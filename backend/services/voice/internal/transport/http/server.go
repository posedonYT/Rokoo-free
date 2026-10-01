package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/pion/webrtc/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/services/voice/internal/registry"
	"rokko.dev/services/voice/internal/sfu"
	"rokko.dev/services/voice/internal/turn"
)

type Access interface {
	Join(ctx context.Context, token, roomID, kind string) (members []string, guildID string, err error)
}

type Publisher interface {
	Publish(ctx context.Context, topic, key string, payload []byte) error
}

type Options struct {
	Tokens             *authn.Manager
	Access             Access
	Pub                Publisher
	ICE                []webrtc.ICEServer
	API                *webrtc.API
	Registry           *registry.Registry
	NodeID             string
	PublicURL          string
	Log                *zap.Logger
	MaxParticipants    int
	MaxVideoPublishers int
	STUN               string
	TURNURL            string
	TURNSecret         string
	TURNTTl            time.Duration
	RingTimeout        time.Duration
	Origins            []string
	Ready              func(context.Context) error
}

type Server struct {
	opts      Options
	mu        sync.Mutex
	rooms     map[string]*sfu.Room
	conns     map[string]context.CancelFunc
	draining  atomic.Bool
	writeQ    atomic.Int64
	metrics   *metrics
	pubCh     chan pubJob
	joinLimit *rate.Limiter
}

type pubJob struct {
	topic, key string
	payload    []byte
}

func New(opts Options) *Server {
	if opts.MaxParticipants <= 0 {
		opts.MaxParticipants = 25
	}
	if opts.MaxVideoPublishers <= 0 {
		opts.MaxVideoPublishers = 9
	}
	if opts.RingTimeout <= 0 {
		opts.RingTimeout = 45 * time.Second
	}
	if opts.TURNTTl <= 0 {
		opts.TURNTTl = 10 * time.Minute
	}
	if opts.NodeID == "" {
		opts.NodeID = "local"
	}
	if opts.Log == nil {
		opts.Log = zap.NewNop()
	}
	if len(opts.Origins) == 0 {
		opts.Origins = []string{"localhost:*", "127.0.0.1:*"}
	}
	reg := prometheus.NewRegistry()
	s := &Server{
		opts:      opts,
		rooms:     map[string]*sfu.Room{},
		conns:     map[string]context.CancelFunc{},
		metrics:   newMetrics(reg),
		pubCh:     make(chan pubJob, 256),
		joinLimit: rate.NewLimiter(rate.Every(100*time.Millisecond), 5),
	}
	go s.publishLoop()
	return s
}

func (s *Server) publishLoop() {
	for job := range s.pubCh {
		if s.opts.Pub == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := s.opts.Pub.Publish(ctx, job.topic, job.key, job.payload); err != nil {
			s.opts.Log.Warn("publish", zap.Error(err), zap.String("topic", job.topic), zap.String("key", job.key))
		}
		cancel()
	}
}

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/ready", s.ready)
	r.GET("/metrics", func(c *gin.Context) {
		s.Collect()
		promhttp.HandlerFor(s.metrics.reg, promhttp.HandlerOpts{}).ServeHTTP(c.Writer, c.Request)
	})
	r.GET("/ws", s.ws)
	r.GET("/rooms/dm/:id", s.participants)
	r.GET("/rooms/guild/:id", s.guildParticipants)
	r.GET("/rooms/resolve", s.resolve)
	return r
}

func (s *Server) ready(c *gin.Context) {
	if s.draining.Load() {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if s.opts.Ready != nil {
		if err := s.opts.Ready(c.Request.Context()); err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
	}
	c.Status(http.StatusOK)
}

func (s *Server) RefreshRooms(ctx context.Context) {
	if s.opts.Registry == nil {
		return
	}
	s.mu.Lock()
	rooms := make(map[string]*sfu.Room, len(s.rooms))
	for id, room := range s.rooms {
		rooms[id] = room
	}
	load := len(s.rooms)
	s.mu.Unlock()
	for id, room := range rooms {
		_ = s.opts.Registry.SetParticipants(ctx, id, room.Peers())
		_, _ = s.opts.Registry.Refresh(ctx, id, s.opts.NodeID)
	}
	_ = s.opts.Registry.UpsertNode(ctx, registry.Node{ID: s.opts.NodeID, URL: s.opts.PublicURL, Load: load, Draining: s.draining.Load()})
}

func (s *Server) Drain(ctx context.Context) {
	s.draining.Store(true)
	if s.opts.Registry != nil {
		_ = s.opts.Registry.UpsertNode(ctx, registry.Node{ID: s.opts.NodeID, URL: s.opts.PublicURL, Draining: true})
	}
	s.mu.Lock()
	rooms := make(map[string]*sfu.Room, len(s.rooms))
	for id, room := range s.rooms {
		rooms[id] = room
	}
	s.mu.Unlock()
	for _, room := range rooms {
		for _, peer := range room.Peers() {
			room.Kick(peer, "reconnect")
		}
	}
	<-ctx.Done()
	s.mu.Lock()
	for id, room := range s.rooms {
		if s.opts.Registry != nil {
			_ = s.opts.Registry.Release(context.Background(), id, s.opts.NodeID)
		}
		for _, peer := range room.Peers() {
			room.ForceLeave(peer)
		}
		delete(s.rooms, id)
	}
	s.mu.Unlock()
}

func (s *Server) HandleDeclined(ctx context.Context, roomID, userID string) {
	key := "dm:" + roomID
	s.mu.Lock()
	room := s.rooms[key]
	s.mu.Unlock()
	if room == nil {
		return
	}
	peers := room.Peers()
	if len(peers) == 1 {
		room.Kick(peers[0], "declined")
		s.dropIfEmpty(key, room)
	}
}

func (s *Server) participants(c *gin.Context) {
	s.roomParticipants(c, "dm")
}

func (s *Server) guildParticipants(c *gin.Context) {
	s.roomParticipants(c, "guild")
}

func (s *Server) roomParticipants(c *gin.Context, kind string) {
	c.Header("Access-Control-Allow-Origin", "*")
	raw := bearer(c)
	if _, err := s.opts.Tokens.Parse(raw); err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	roomID := c.Param("id")
	if _, _, err := s.opts.Access.Join(c.Request.Context(), raw, roomID, kind); err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	c.JSON(http.StatusOK, gin.H{"participants": s.participantIDs(c.Request.Context(), kind+":"+roomID)})
}

func (s *Server) resolve(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	raw := bearer(c)
	if _, err := s.opts.Tokens.Parse(raw); err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	kind := c.Query("kind")
	roomID := c.Query("id")
	if kind == "" || roomID == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	if _, _, err := s.opts.Access.Join(c.Request.Context(), raw, roomID, kind); err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	if s.draining.Load() && s.opts.Registry == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	key := kind + ":" + roomID
	url := s.opts.PublicURL
	if s.opts.Registry != nil {
		owner, err := s.pickOwner(c.Request.Context(), key)
		if err != nil {
			s.opts.Log.Warn("resolve", zap.Error(err), zap.String("room", key))
			c.Status(http.StatusServiceUnavailable)
			return
		}
		nodes, err := s.opts.Registry.Nodes(c.Request.Context())
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		for _, n := range nodes {
			if n.ID == owner {
				url = n.URL
				break
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"ws_url": url, "node_id": s.opts.NodeID, "room": key})
}

func (s *Server) pickOwner(ctx context.Context, room string) (string, error) {
	owner, err := s.opts.Registry.Owner(ctx, room)
	if err != nil {
		return "", err
	}
	if owner != "" {
		return owner, nil
	}
	if s.draining.Load() {
		nodes, err := s.opts.Registry.Nodes(ctx)
		if err != nil {
			return "", err
		}
		best := ""
		bestLoad := int(^uint(0) >> 1)
		for _, n := range nodes {
			if n.Draining {
				continue
			}
			if n.Load < bestLoad {
				best = n.ID
				bestLoad = n.Load
			}
		}
		if best == "" {
			return "", fmt.Errorf("no voice node")
		}
		return s.opts.Registry.Claim(ctx, room, best)
	}
	return s.opts.Registry.Claim(ctx, room, s.opts.NodeID)
}

type joinMessage struct {
	Type   string `json:"type"`
	RoomID string `json:"room_id"`
	Kind   string `json:"kind"`
}

func (s *Server) ws(c *gin.Context) {
	raw := tokenFromRequest(c.Request)
	idnty, err := s.opts.Tokens.Parse(raw)
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	if !s.joinLimit.Allow() {
		c.Status(http.StatusTooManyRequests)
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		OriginPatterns: s.opts.Origins,
		Subprotocols:   []string{"rokko.v1"},
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 16)
	defer conn.Close(websocket.StatusNormalClosure, "")
	writer := newWriter(conn)
	defer writer.Close()
	ctx := c.Request.Context()
	joinCtx, cancelJoin := context.WithTimeout(ctx, 10*time.Second)
	_, data, err := conn.Read(joinCtx)
	cancelJoin()
	if err != nil {
		return
	}
	var join joinMessage
	if json.Unmarshal(data, &join) != nil || join.Type != "join" || join.RoomID == "" || (join.Kind != "dm" && join.Kind != "guild") {
		return
	}
	members, guildID, err := s.opts.Access.Join(ctx, raw, join.RoomID, join.Kind)
	if err != nil {
		_ = writer.Send(mustJSON(map[string]string{"type": "error", "error": err.Error()}))
		return
	}
	if s.draining.Load() {
		_ = writer.Send(mustJSON(map[string]string{"type": "reconnect"}))
		return
	}
	userID := id.Format(idnty.UserID)
	key := join.Kind + ":" + join.RoomID
	if s.opts.Registry != nil {
		owner, err := s.opts.Registry.Owner(ctx, key)
		if err != nil || (owner != "" && owner != s.opts.NodeID) {
			_ = writer.Send(mustJSON(map[string]string{"type": "reconnect"}))
			return
		}
		if owner == "" {
			got, err := s.opts.Registry.Claim(ctx, key, s.opts.NodeID)
			if err != nil || got != s.opts.NodeID {
				_ = writer.Send(mustJSON(map[string]string{"type": "reconnect"}))
				return
			}
		}
	}
	ice := turn.ICEServers(s.opts.STUN, s.opts.TURNURL, s.opts.TURNSecret, s.opts.TURNTTl, time.Now())
	_ = writer.Send(mustJSON(map[string]any{"type": "config", "ice_servers": ice}))
	send := func(_ string, payload []byte) {
		if !writer.Send(payload) {
			s.opts.Log.Warn("ws queue full", zap.String("user", userID), zap.String("room", key), zap.String("node", s.opts.NodeID))
			_ = conn.Close(websocket.StatusPolicyViolation, "slow consumer")
		}
		s.writeQ.Store(int64(writer.Depth()))
	}
	room, sess, first, err := s.joinRoom(key, userID, send)
	if err != nil {
		if sfu.IsRoomFull(err) {
			_ = writer.Send(mustJSON(map[string]string{"type": "error", "error": "room_full"}))
		}
		return
	}
	s.mu.Lock()
	connCtx, connCancel := context.WithCancel(context.Background())
	s.conns[key+"|"+userID] = connCancel
	s.mu.Unlock()
	defer func() {
		connCancel()
		s.mu.Lock()
		delete(s.conns, key+"|"+userID)
		s.mu.Unlock()
		empty := room.Leave(sess)
		if empty {
			s.dropIfEmpty(key, room)
		}
		s.syncParticipants(context.Background(), key, room)
		s.publishState(context.Background(), userID, join, guildID, members, s.participantIDs(context.Background(), key), "left")
	}()
	s.syncParticipants(ctx, key, room)
	s.publishState(ctx, userID, join, guildID, members, room.Peers(), "joined")
	if first && join.Kind == "dm" {
		s.ring(ctx, userID, join, members)
		go s.watchRing(room, key, userID, join, members)
	}
	go s.keepalive(connCtx, conn)
	if !idnty.ExpiresAt.IsZero() {
		go s.closeOnExpiry(connCtx, conn, idnty.ExpiresAt)
	}
	signalLimit := rate.NewLimiter(40, 40)
	s.opts.Log.Info("joined", zap.String("room", key), zap.String("user", userID), zap.String("node", s.opts.NodeID))
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if !signalLimit.Allow() {
			_ = conn.Close(websocket.StatusPolicyViolation, "rate limit")
			return
		}
		if err := room.Signal(userID, payload); err != nil {
			s.opts.Log.Warn("signal", zap.Error(err), zap.String("room", key), zap.String("user", userID))
		}
	}
}

func (s *Server) joinRoom(key, userID string, send sfu.Signal) (*sfu.Room, *sfu.Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[key]
	created := false
	if room == nil {
		room = sfu.NewRoom(key, s.opts.API, s.opts.ICE)
		room.SetLimits(s.opts.MaxParticipants, s.opts.MaxVideoPublishers)
		room.OnError(func(msg string, err error) {
			s.opts.Log.Warn(msg, zap.Error(err), zap.String("room", key), zap.String("node", s.opts.NodeID))
		})
		s.rooms[key] = room
		created = true
	}
	first := room.Empty()
	sess, err := room.Join(userID, send)
	if err != nil {
		if created && room.Empty() {
			delete(s.rooms, key)
		}
		return nil, nil, false, err
	}
	return room, sess, first, nil
}

func (s *Server) closeOnExpiry(ctx context.Context, conn *websocket.Conn, exp time.Time) {
	d := time.Until(exp)
	if d <= 0 {
		_ = conn.Close(websocket.StatusPolicyViolation, "token expired")
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-t.C:
		_ = conn.Close(websocket.StatusPolicyViolation, "token expired")
	}
}

func (s *Server) keepalive(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ping, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := conn.Ping(ping)
			cancel()
			if err != nil {
				_ = conn.Close(websocket.StatusGoingAway, "ping failed")
				return
			}
		}
	}
}

func (s *Server) watchRing(room *sfu.Room, key, userID string, join joinMessage, members []string) {
	timer := time.NewTimer(s.opts.RingTimeout)
	defer timer.Stop()
	<-timer.C
	peers := room.Peers()
	if len(peers) != 1 || peers[0] != userID {
		return
	}
	s.publish(context.Background(), "voice.call.missed", join.RoomID, "", members, map[string]string{
		"room_id": join.RoomID, "from_id": userID,
	})
	room.Kick(userID, "ring_timeout")
	s.dropIfEmpty(key, room)
}

func (s *Server) dropIfEmpty(id string, room *sfu.Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rooms[id] == room && room.Empty() {
		delete(s.rooms, id)
		if s.opts.Registry != nil {
			_ = s.opts.Registry.Release(context.Background(), id, s.opts.NodeID)
		}
	}
}

func (s *Server) participantIDs(ctx context.Context, room string) []string {
	if s.opts.Registry != nil {
		ids, err := s.opts.Registry.Participants(ctx, room)
		if err == nil {
			return ids
		}
	}
	s.mu.Lock()
	current := s.rooms[room]
	s.mu.Unlock()
	if current == nil {
		return []string{}
	}
	return current.Peers()
}

func (s *Server) syncParticipants(ctx context.Context, room string, current *sfu.Room) {
	if s.opts.Registry == nil {
		return
	}
	_ = s.opts.Registry.SetParticipants(ctx, room, current.Peers())
	_, _ = s.opts.Registry.Refresh(ctx, room, s.opts.NodeID)
	s.mu.Lock()
	load := len(s.rooms)
	s.mu.Unlock()
	_ = s.opts.Registry.UpsertNode(ctx, registry.Node{ID: s.opts.NodeID, URL: s.opts.PublicURL, Load: load})
}

func (s *Server) ring(ctx context.Context, userID string, join joinMessage, members []string) {
	if s.opts.Pub == nil || join.Kind != "dm" {
		return
	}
	var targets []string
	for _, member := range members {
		if member != userID {
			targets = append(targets, member)
		}
	}
	if len(targets) == 0 {
		return
	}
	s.publish(ctx, "voice.call.ring", join.RoomID, "", targets, map[string]string{
		"room_id": join.RoomID, "from_id": userID,
	})
}

func (s *Server) publishState(ctx context.Context, userID string, join joinMessage, guildID string, members, participants []string, state string) {
	if s.opts.Pub == nil {
		return
	}
	recipients := members
	if join.Kind == "guild" {
		recipients = nil
	}
	if join.Kind == "dm" && len(members) == 0 {
		return
	}
	s.publish(ctx, "voice.state.updated", join.RoomID, guildID, recipients, map[string]string{
		"room_id":      join.RoomID,
		"user_id":      userID,
		"state":        state,
		"participants": strings.Join(participants, ","),
	})
}

func (s *Server) publish(ctx context.Context, typ, roomID, guildID string, recipients []string, payload map[string]string) {
	if s.opts.Pub == nil {
		return
	}
	env, err := eventx.New(fmt.Sprintf("%s:%s:%d", typ, roomID, time.Now().UnixNano()), typ, payload)
	if err != nil {
		return
	}
	env.RecipientIDs = recipients
	env.ChannelID = roomID
	env.GuildID = guildID
	raw, err := env.Bytes()
	if err != nil {
		return
	}
	job := pubJob{topic: "voice.events", key: roomID, payload: raw}
	select {
	case s.pubCh <- job:
	default:
		s.opts.Log.Warn("publish queue full", zap.String("type", typ), zap.String("room", roomID))
	}
}

func (s *Server) Collect() {
	s.mu.Lock()
	rooms := make([]*sfu.Room, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, room)
	}
	q := s.writeQ.Load()
	s.mu.Unlock()
	peers, tracks := 0, 0
	var in, out, switches, pli uint64
	bwe := 0
	for _, room := range rooms {
		st := room.Stats()
		peers += st.Peers
		tracks += st.Tracks
		in += st.BytesIn
		out += st.BytesOut
		switches += st.LayerSwitches
		pli += st.PLI
		if st.BWE > bwe {
			bwe = st.BWE
		}
	}
	s.metrics.rooms.Set(float64(len(rooms)))
	s.metrics.peers.Set(float64(peers))
	s.metrics.tracks.Set(float64(tracks))
	s.metrics.bytesIn.Set(float64(in))
	s.metrics.bytesOut.Set(float64(out))
	s.metrics.switches.Set(float64(switches))
	s.metrics.pli.Set(float64(pli))
	s.metrics.bwe.Set(float64(bwe))
	s.metrics.queue.Set(float64(q))
}

func bearer(c *gin.Context) string {
	return tokenFromRequest(c.Request)
}

func tokenFromRequest(r *http.Request) string {
	for _, line := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(line, ",") {
			p = strings.TrimSpace(p)
			if p != "" && p != "rokko.v1" {
				return p
			}
		}
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return strings.TrimPrefix(q, "Bearer ")
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
