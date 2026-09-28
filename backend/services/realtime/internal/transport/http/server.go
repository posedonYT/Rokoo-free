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
	userv1 "rokko.dev/gen/user/v1"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/id"
	"rokko.dev/services/realtime/internal/dispatch"
	"rokko.dev/services/realtime/internal/hub"
	"rokko.dev/services/realtime/internal/state"
)

type Server struct {
	tokens  *authn.Manager
	hub     *hub.Hub
	state   *state.Redis
	users   userv1.UserServiceClient
	publish func(ctx context.Context, userID string, payload []byte) error
	events  func(ctx context.Context, topic, key string, payload []byte) error
	log     func(msg string, err error)
	subMu   sync.Mutex
	ps      *redis.PubSub
	refs    map[string]int
}

func New(tokens *authn.Manager, h *hub.Hub, st *state.Redis, users userv1.UserServiceClient) *Server {
	return &Server{tokens: tokens, hub: h, state: st, users: users, publish: st.Publish, refs: map[string]int{}}
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
	if ok, err := s.state.Once(ctx, env.ID, 24*time.Hour); err != nil {
		s.warn("dedup", err)
	} else if !ok {
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
	for _, userID := range ids {
		_ = s.publish(ctx, userID, body)
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

func (s *Server) ws(c *gin.Context) {
	raw := strings.TrimPrefix(c.Query("token"), "Bearer ")
	if header := c.GetHeader("Authorization"); raw == "" {
		raw = strings.TrimPrefix(header, "Bearer ")
	}
	idnty, err := s.tokens.Parse(raw)
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	sock := &socket{userID: id.Format(idnty.UserID), connID: strconv.FormatInt(time.Now().UnixNano(), 10), conn: conn, send: make(chan []byte, 32)}
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
	s.notifyFriends(ctx, raw, sock.userID, "online")
	defer s.notifyFriends(context.Background(), raw, sock.userID, "offline")

	var subs struct{ guilds, dms []string }
	defer func() { _ = s.state.Unsubscribe(context.Background(), sock.userID, subs.guilds, subs.dms) }()

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
			if err := s.state.Touch(ctx, sock.userID, sock.connID, subs.guilds, subs.dms, 45*time.Second); err != nil {
				s.warn("heartbeat", err)
			}
			_ = writeJSON(ctx, conn, map[string]string{"type": "heartbeat_ack"})
		case "subscribe":
			if err := s.state.Unsubscribe(ctx, sock.userID, subs.guilds, subs.dms); err != nil {
				s.warn("unsubscribe", err)
			}
			subs.guilds, subs.dms = msg.GuildIDs, msg.DMIDs
			if err := s.state.Touch(ctx, sock.userID, sock.connID, subs.guilds, subs.dms, 45*time.Second); err != nil {
				s.warn("subscribe", err)
			}
		case "typing":
			s.broadcastTyping(ctx, sock.userID, msg.ChannelID, msg.GuildID)
		case "call_decline":
			s.declineCall(ctx, sock.userID, msg.ChannelID)
		case "presence_query":
			online, _ := s.state.Online(ctx, msg.UserIDs)
			_ = writeJSON(ctx, conn, map[string]any{"type": "presence_snapshot", "online": online})
		}
	}
}

func (s *Server) notifyFriends(ctx context.Context, token, userID, status string) {
	if s.users == nil {
		return
	}
	resp, err := s.users.ListFriendIDs(grpcx.WithToken(ctx, token), &userv1.ListRequest{})
	if err != nil {
		return
	}
	body, _ := json.Marshal(map[string]string{"type": "presence", "user_id": userID, "status": status})
	for _, friendID := range resp.GetIds() {
		_ = s.publish(ctx, friendID, body)
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

func (s *Server) declineCall(ctx context.Context, userID, channelID string) {
	if channelID == "" {
		return
	}
	targets, err := s.state.DMUsers(ctx, channelID)
	if err != nil {
		s.warn("decline targets", err)
		return
	}
	if !contains(targets, userID) {
		return
	}
	body, _ := json.Marshal(map[string]string{"type": "call_declined", "channel_id": channelID, "user_id": userID})
	for _, id := range targets {
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

func contains(items []string, v string) bool {
	for _, item := range items {
		if item == v {
			return true
		}
	}
	return false
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}

func (s *Server) ListenRedis(ctx context.Context) {
	pubsub := s.state.Client().Subscribe(ctx, "rt:ctl")
	s.subMu.Lock()
	s.ps = pubsub
	s.subMu.Unlock()
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
