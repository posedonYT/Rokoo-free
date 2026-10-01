package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	guildv1 "rokko.dev/gen/guild/v1"
	messagev1 "rokko.dev/gen/message/v1"
	userv1 "rokko.dev/gen/user/v1"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/id"
	"rokko.dev/services/realtime/internal/dispatch"
	"rokko.dev/services/realtime/internal/hub"
	"rokko.dev/services/realtime/internal/state"
)

const (
	wsProtocol     = "rokko.v1"
	maxSubscribe   = 200
	maxPresenceIDs = 100
	grpcTimeout    = 5 * time.Second
)

var defaultOrigins = []string{"localhost:*", "127.0.0.1:*"}

type Server struct {
	tokens   *authn.Manager
	hub      *hub.Hub
	state    *state.Redis
	users    userv1.UserServiceClient
	guilds   guildv1.GuildServiceClient
	messages messagev1.MessageServiceClient
	origins  []string
	publish  func(ctx context.Context, userID string, payload []byte) error
	events   func(ctx context.Context, topic, key string, payload []byte) error
	log      func(msg string, err error)
	subMu    sync.Mutex
	ps       *redis.PubSub
	refs     map[string]int
}

func New(tokens *authn.Manager, h *hub.Hub, st *state.Redis, users userv1.UserServiceClient, guilds guildv1.GuildServiceClient, messages messagev1.MessageServiceClient) *Server {
	return &Server{tokens: tokens, hub: h, state: st, users: users, guilds: guilds, messages: messages, publish: st.Publish, refs: map[string]int{}, origins: defaultOrigins}
}

func (s *Server) SetOrigins(origins []string) {
	if len(origins) > 0 {
		s.origins = origins
	}
}

func (s *Server) SetEvents(fn func(ctx context.Context, topic, key string, payload []byte) error) {
	s.events = fn
}

func (s *Server) SetLog(fn func(msg string, err error)) { s.log = fn }

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/ws", s.ws)
	return r
}

func (s *Server) Deliver(ctx context.Context, env eventx.Envelope) error {
	if seen, err := s.state.Seen(ctx, env.ID); err != nil {
		s.warn("dedup", err)
	} else if seen {
		return nil
	}
	raw, err := env.Bytes()
	if err != nil {
		return err
	}
	ids := dispatch.Recipients(env, func(guildID string) []string {
		users, err := s.state.GuildUsers(ctx, guildID)
		if err != nil {
			s.warn("guild users", err)
			return nil
		}
		return users
	}, func(channelID string) []string {
		users, err := s.state.DMUsers(ctx, channelID)
		if err != nil {
			s.warn("dm users", err)
			return nil
		}
		return users
	})
	body, _ := json.Marshal(map[string]any{"type": "event", "event_type": env.Type, "payload": json.RawMessage(raw)})
	var firstErr error
	for _, userID := range ids {
		if err := s.publish(ctx, userID, body); err != nil {
			s.warn("publish", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if _, err := s.state.Once(ctx, env.ID, 24*time.Hour); err != nil {
		s.warn("dedup", err)
	}
	return nil
}

type clientMsg struct {
	Type      string   `json:"type"`
	GuildIDs  []string `json:"guild_ids"`
	DMIDs     []string `json:"dm_ids"`
	ChannelID string   `json:"channel_id"`
	GuildID   string   `json:"guild_id"`
	UserIDs   []string `json:"user_ids"`
}

type socket struct {
	userID string
	connID string
	conn   *websocket.Conn
	send   chan []byte
}

func (s *socket) UserID() string { return s.userID }
func (s *socket) Send(payload []byte) bool {
	select {
	case s.send <- payload:
		return true
	default:
		return false
	}
}

func (s *socket) Close() { _ = s.conn.CloseNow() }

type connState struct {
	token     string
	userID    string
	connID    string
	guilds    map[string]struct{}
	dms       map[string]struct{}
	subGuilds []string
	subDMs    []string
	channels  map[string]string
	friends   map[string]struct{}
	friendIDs []string
}

func tokenFromRequest(r *http.Request) string {
	for _, line := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(line, ",") {
			p = strings.TrimSpace(p)
			if p != "" && p != wsProtocol {
				return p
			}
		}
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return strings.TrimPrefix(q, "Bearer ")
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Server) ws(c *gin.Context) {
	raw := tokenFromRequest(c.Request)
	idnty, err := s.tokens.Parse(raw)
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		OriginPatterns: s.origins,
		Subprotocols:   []string{wsProtocol},
	})
	if err != nil {
		return
	}
	sock := &socket{userID: id.Format(idnty.UserID), connID: strconv.FormatInt(time.Now().UnixNano(), 10), conn: conn, send: make(chan []byte, 32)}
	st := &connState{
		token: raw, userID: sock.userID, connID: sock.connID,
		guilds: map[string]struct{}{}, dms: map[string]struct{}{},
		channels: map[string]string{}, friends: map[string]struct{}{},
	}
	if !idnty.ExpiresAt.IsZero() {
		timer := time.AfterFunc(time.Until(idnty.ExpiresAt), func() {
			_ = conn.Close(websocket.StatusPolicyViolation, "token expired")
		})
		defer timer.Stop()
	}
	s.hub.Add(sock)
	s.track(sock.userID, 1)
	ctx := c.Request.Context()
	if err := s.state.SetOnline(ctx, sock.userID, sock.connID, 45*time.Second); err != nil {
		s.warn("presence", err)
	}
	defer func() {
		if err := s.state.SetOffline(context.Background(), sock.userID, sock.connID); err != nil {
			s.warn("presence", err)
		}
	}()
	s.loadFriends(ctx, st)
	s.notifyFriends(ctx, st.friendIDs, sock.userID, "online")
	defer s.notifyFriends(context.Background(), st.friendIDs, sock.userID, "offline")

	defer func() {
		_ = s.state.Unsubscribe(context.Background(), sock.userID, sock.connID, st.subGuilds, st.subDMs)
	}()

	go func() {
		for payload := range sock.send {
			writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
		}
	}()
	defer close(sock.send)
	defer s.hub.Remove(sock)
	defer s.track(sock.userID, -1)
	_ = writeJSON(ctx, conn, map[string]string{"type": "ready", "user_id": sock.userID})

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg clientMsg
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "heartbeat":
			if err := s.state.Touch(ctx, sock.userID, sock.connID, st.subGuilds, st.subDMs, 45*time.Second); err != nil {
				s.warn("heartbeat", err)
			}
			_ = writeJSON(ctx, conn, map[string]string{"type": "heartbeat_ack"})
		case "subscribe":
			if err := s.state.Unsubscribe(ctx, sock.userID, sock.connID, st.subGuilds, st.subDMs); err != nil {
				s.warn("unsubscribe", err)
			}
			st.subGuilds, st.subDMs = s.authorizeSubscriptions(ctx, st, msg.GuildIDs, msg.DMIDs)
			if err := s.state.Touch(ctx, sock.userID, sock.connID, st.subGuilds, st.subDMs, 45*time.Second); err != nil {
				s.warn("subscribe", err)
			}
		case "typing":
			if s.typingAllowed(ctx, st, msg.ChannelID, msg.GuildID) {
				s.broadcastTyping(ctx, sock.userID, msg.ChannelID, msg.GuildID)
			}
		case "call_decline":
			s.declineCall(ctx, st, msg.ChannelID)
		case "presence_query":
			ids := make([]string, 0, len(msg.UserIDs))
			for _, uid := range msg.UserIDs {
				if len(ids) >= maxPresenceIDs {
					break
				}
				if _, ok := st.friends[uid]; ok || uid == sock.userID {
					ids = append(ids, uid)
				}
			}
			online, _ := s.state.Online(ctx, ids)
			_ = writeJSON(ctx, conn, map[string]any{"type": "presence_snapshot", "online": online})
		}
	}
}

func capUnique(in []string, limit int) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		if len(out) >= limit {
			break
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func (s *Server) authorizeSubscriptions(ctx context.Context, st *connState, guildIDs, dmIDs []string) ([]string, []string) {
	guildIDs = capUnique(guildIDs, maxSubscribe)
	dmIDs = capUnique(dmIDs, maxSubscribe)
	st.guilds = map[string]struct{}{}
	st.dms = map[string]struct{}{}
	var guilds, dms []string
	if len(guildIDs) > 0 && s.guilds != nil {
		gctx, cancel := context.WithTimeout(grpcx.WithToken(ctx, st.token), grpcTimeout)
		resp, err := s.guilds.ListMyGuilds(gctx, &guildv1.Empty{})
		cancel()
		if err != nil {
			s.warn("list my guilds", err)
		} else {
			member := make(map[string]struct{}, len(resp.GetGuilds()))
			for _, g := range resp.GetGuilds() {
				member[g.GetId()] = struct{}{}
			}
			for _, gid := range guildIDs {
				if _, ok := member[gid]; ok {
					guilds = append(guilds, gid)
					st.guilds[gid] = struct{}{}
				}
			}
		}
	}
	if len(dmIDs) > 0 && s.messages != nil {
		allowed := make([]bool, len(dmIDs))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for i, dmID := range dmIDs {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, dmID string) {
				defer wg.Done()
				defer func() { <-sem }()
				allowed[i] = s.dmAllowed(ctx, st.token, dmID, nil)
			}(i, dmID)
		}
		wg.Wait()
		for i, dmID := range dmIDs {
			if allowed[i] {
				dms = append(dms, dmID)
				st.dms[dmID] = struct{}{}
			}
		}
	}
	return guilds, dms
}

func (s *Server) dmAllowed(ctx context.Context, token, dmID string, members *[]string) bool {
	if s.messages == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(grpcx.WithToken(ctx, token), grpcTimeout)
	defer cancel()
	resp, err := s.messages.CheckDMAccess(cctx, &messagev1.CheckDMAccessRequest{ChannelId: dmID})
	if err != nil {
		s.warn("check dm access", err)
		return false
	}
	if !resp.GetAllowed() {
		return false
	}
	if members != nil {
		*members = resp.GetMemberIds()
	}
	return true
}

func (s *Server) typingAllowed(ctx context.Context, st *connState, channelID, guildID string) bool {
	if channelID == "" {
		return false
	}
	if guildID == "" {
		_, ok := st.dms[channelID]
		return ok
	}
	if _, ok := st.guilds[guildID]; !ok {
		return false
	}
	if g, ok := st.channels[channelID]; ok {
		return g == guildID
	}
	if s.guilds == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(grpcx.WithToken(ctx, st.token), grpcTimeout)
	defer cancel()
	resp, err := s.guilds.CheckChannelAccess(cctx, &guildv1.CheckChannelAccessRequest{ChannelId: channelID})
	if err != nil {
		s.warn("check channel access", err)
		return false
	}
	if !resp.GetAllowed() || resp.GetGuildId() != guildID {
		return false
	}
	if len(st.channels) < 1000 {
		st.channels[channelID] = guildID
	}
	return true
}

func (s *Server) loadFriends(ctx context.Context, st *connState) {
	if s.users == nil {
		return
	}
	cctx, cancel := context.WithTimeout(grpcx.WithToken(ctx, st.token), grpcTimeout)
	defer cancel()
	resp, err := s.users.ListFriendIDs(cctx, &userv1.ListRequest{})
	if err != nil {
		s.warn("list friends", err)
		return
	}
	st.friendIDs = resp.GetIds()
	for _, fid := range st.friendIDs {
		st.friends[fid] = struct{}{}
	}
}

func (s *Server) notifyFriends(ctx context.Context, friendIDs []string, userID, status string) {
	if len(friendIDs) == 0 {
		return
	}
	body, _ := json.Marshal(map[string]string{"type": "presence", "user_id": userID, "status": status})
	for _, friendID := range friendIDs {
		if err := s.publish(ctx, friendID, body); err != nil {
			s.warn("presence publish", err)
		}
	}
}

func (s *Server) broadcastTyping(ctx context.Context, userID, channelID, guildID string) {
	if channelID == "" {
		return
	}
	var targets []string
	var err error
	if guildID != "" {
		targets, err = s.state.GuildUsers(ctx, guildID)
	} else {
		targets, err = s.state.DMUsers(ctx, channelID)
	}
	if err != nil {
		s.warn("typing targets", err)
		return
	}
	body, _ := json.Marshal(map[string]string{"type": "typing", "channel_id": channelID, "user_id": userID})
	seen := map[string]struct{}{}
	for _, id := range targets {
		if id == userID {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		_ = s.publish(ctx, id, body)
	}
}

func (s *Server) declineCall(ctx context.Context, st *connState, channelID string) {
	if channelID == "" {
		return
	}
	var members []string
	if !s.dmAllowed(ctx, st.token, channelID, &members) {
		return
	}
	userID := st.userID
	body, _ := json.Marshal(map[string]string{"type": "call_declined", "channel_id": channelID, "user_id": userID})
	for _, id := range members {
		if id != userID {
			if err := s.publish(ctx, id, body); err != nil && s.log != nil {
				s.log("decline publish", err)
			}
		}
	}
	if s.events != nil {
		env, err := eventx.New(channelID+":"+userID, "voice.call.declined", map[string]string{
			"room_id": channelID, "user_id": userID,
		})
		if err != nil {
			return
		}
		raw, err := env.Bytes()
		if err != nil {
			return
		}
		if err := s.events(ctx, "voice.events", channelID, raw); err != nil && s.log != nil {
			s.log("decline event", err)
		}
	}
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}

func (s *Server) InitPubSub(ctx context.Context) {
	pubsub := s.state.Client().Subscribe(ctx, "rt:ctl")
	s.subMu.Lock()
	s.ps = pubsub
	s.subMu.Unlock()
}

func (s *Server) ListenRedis(ctx context.Context) {
	s.subMu.Lock()
	pubsub := s.ps
	s.subMu.Unlock()
	if pubsub == nil {
		s.InitPubSub(ctx)
		s.subMu.Lock()
		pubsub = s.ps
		s.subMu.Unlock()
	}
	defer pubsub.Close()
	for {
		msg, err := pubsub.ReceiveMessage(ctx)
		if err != nil {
			return
		}
		if !strings.HasPrefix(msg.Channel, "rt:user:") {
			continue
		}
		userID := strings.TrimPrefix(msg.Channel, "rt:user:")
		s.hub.Send(userID, []byte(msg.Payload))
	}
}

func (s *Server) track(userID string, delta int) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	s.refs[userID] += delta
	if s.ps == nil {
		return
	}
	if delta > 0 && s.refs[userID] == 1 {
		if err := s.ps.Subscribe(context.Background(), "rt:user:"+userID); err != nil {
			s.warn("subscribe redis", err)
		}
	}
	if s.refs[userID] <= 0 {
		delete(s.refs, userID)
		if err := s.ps.Unsubscribe(context.Background(), "rt:user:"+userID); err != nil {
			s.warn("unsubscribe redis", err)
		}
	}
}

func (s *Server) warn(msg string, err error) {
	if s.log != nil && err != nil {
		s.log(msg, err)
	}
}
