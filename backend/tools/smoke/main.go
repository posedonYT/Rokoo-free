package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"google.golang.org/protobuf/proto"
	authv1 "rokko.dev/gen/auth/v1"
	guildv1 "rokko.dev/gen/guild/v1"
	messagev1 "rokko.dev/gen/message/v1"
)

func main() {
	api := env("API", "http://localhost:8080")
	rtURL := env("RT_WS", "ws://localhost:8081/ws")
	voiceURL := env("VOICE_WS", "ws://localhost:8082/ws")
	suf := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	emailA := "smoke-a-" + suf + "@example.com"
	emailB := "smoke-b-" + suf + "@example.com"

	log.Println("register users")
	a := &authv1.AuthResponse{}
	must(grpcWeb(api, "auth.v1.AuthService", "Register", "", &authv1.RegisterRequest{
		Email: emailA, Username: "smokea" + suf[:6], Password: "supersecret1",
	}, a))
	b := &authv1.AuthResponse{}
	must(grpcWeb(api, "auth.v1.AuthService", "Register", "", &authv1.RegisterRequest{
		Email: emailB, Username: "smokeb" + suf[:6], Password: "supersecret1",
	}, b))

	log.Println("create guild + dm")
	guild := &guildv1.Guild{}
	must(grpcWeb(api, "guild.v1.GuildService", "CreateGuild", a.AccessToken, &guildv1.CreateGuildRequest{Name: "Smoke"}, guild))
	channels := &guildv1.ListChannelsResponse{}
	must(grpcWeb(api, "guild.v1.GuildService", "ListChannels", a.AccessToken, &guildv1.GuildRef{GuildId: guild.Id}, channels))
	if len(channels.Channels) == 0 {
		log.Fatal("no channels")
	}
	ch := channels.Channels[0]
	dm := &messagev1.DMChannel{}
	must(grpcWeb(api, "message.v1.MessageService", "OpenDM", a.AccessToken, &messagev1.OpenDMRequest{UserId: b.UserId}, dm))

	log.Println("realtime: ready + presence + typing + message")
	inboxA := make(chan map[string]any, 64)
	inboxB := make(chan map[string]any, 64)
	wsA := mustWS(rtURL, a.AccessToken, inboxA)
	defer wsA.Close(websocket.StatusNormalClosure, "")
	wsB := mustWS(rtURL, b.AccessToken, inboxB)
	defer wsB.Close(websocket.StatusNormalClosure, "")
	waitType(inboxA, "ready", 5*time.Second)
	waitType(inboxB, "ready", 5*time.Second)
	sendJSON(wsA, map[string]any{"type": "subscribe", "guild_ids": []string{guild.Id}, "dm_ids": []string{dm.Id}})
	sendJSON(wsB, map[string]any{"type": "subscribe", "guild_ids": []string{}, "dm_ids": []string{dm.Id}})
	time.Sleep(800 * time.Millisecond)

	sendJSON(wsB, map[string]any{"type": "subscribe", "guild_ids": []string{guild.Id}, "dm_ids": []string{dm.Id}})
	time.Sleep(500 * time.Millisecond)
	drainEvents(inboxA, 300*time.Millisecond)
	drainEvents(inboxB, 300*time.Millisecond)
	msg := &messagev1.Message{}
	must(grpcWeb(api, "message.v1.MessageService", "SendMessage", a.AccessToken, &messagev1.SendMessageRequest{
		ChannelId: ch.Id, ChannelKind: "guild", Content: "hello-guild",
	}, msg))
	waitEvent(inboxA, "message.created", 15*time.Second)
	assertNoEvent(inboxB, "message.created", 2*time.Second)

	sendJSON(wsA, map[string]any{"type": "typing", "channel_id": dm.Id})
	waitType(inboxB, "typing", 5*time.Second)

	must(grpcWeb(api, "message.v1.MessageService", "SendMessage", a.AccessToken, &messagev1.SendMessageRequest{
		ChannelId: dm.Id, ChannelKind: "dm", Content: "hello-dm",
	}, &messagev1.Message{}))
	waitEvent(inboxB, "message.created", 10*time.Second)

	log.Println("realtime: two tabs, close one")
	inboxA2 := make(chan map[string]any, 64)
	wsA2 := mustWS(rtURL, a.AccessToken, inboxA2)
	waitType(inboxA2, "ready", 5*time.Second)
	sendJSON(wsA2, map[string]any{"type": "subscribe", "guild_ids": []string{guild.Id}, "dm_ids": []string{dm.Id}})
	time.Sleep(300 * time.Millisecond)
	_ = wsA.Close(websocket.StatusNormalClosure, "")
	time.Sleep(400 * time.Millisecond)
	must(grpcWeb(api, "message.v1.MessageService", "SendMessage", b.AccessToken, &messagev1.SendMessageRequest{
		ChannelId: dm.Id, ChannelKind: "dm", Content: "after-close",
	}, &messagev1.Message{}))
	waitEvent(inboxA2, "message.created", 10*time.Second)
	_ = wsA2.Close(websocket.StatusNormalClosure, "")

	log.Println("voice: join + media + reconnect")
	runVoice(voiceURL, a.AccessToken, b.AccessToken, dm.Id)

	log.Println("voice: ring decline")
	runDecline(rtURL, voiceURL, a, b, dm.Id)

	log.Println("SMOKE OK")
}

type voiceSide struct {
	ws    *websocket.Conn
	inbox chan map[string]any
	pc    *webrtc.PeerConnection
}

func runVoice(voiceURL, tokenA, tokenB, roomID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	mk := func(token string) *voiceSide {
		inbox := make(chan map[string]any, 64)
		ws := mustWS(voiceURL, token, inbox)
		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		must(err)
		_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv})
		must(err)
		s := &voiceSide{ws: ws, inbox: inbox, pc: pc}
		pc.OnICECandidate(func(c *webrtc.ICECandidate) {
			if c == nil {
				return
			}
			init := c.ToJSON()
			sendJSON(ws, map[string]any{"type": "candidate", "candidate": map[string]any{
				"candidate": init.Candidate, "sdpMid": init.SDPMid, "sdpMLineIndex": init.SDPMLineIndex,
			}})
		})
		sendJSON(ws, map[string]any{"type": "join", "room_id": roomID, "kind": "dm"})
		waitType(inbox, "config", 8*time.Second)
		waitType(inbox, "joined", 8*time.Second)
		go pumpVoice(ctx, s)
		return s
	}
	a := mk(tokenA)
	b := mk(tokenB)
	defer a.ws.Close(websocket.StatusNormalClosure, "")
	defer b.ws.Close(websocket.StatusNormalClosure, "")
	defer a.pc.Close()
	defer b.pc.Close()

	got := make(chan struct{}, 1)
	b.pc.OnTrack(func(*webrtc.TrackRemote, *webrtc.RTPReceiver) {
		select {
		case got <- struct{}{}:
		default:
		}
	})
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", "stream")
	must(err)
	_, err = a.pc.AddTrack(track)
	must(err)
	must(voiceOffer(a))

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_ = track.WriteSample(media.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond})
		select {
		case <-got:
			log.Println("voice media forwarded")
			goto reconnect
		case <-time.After(40 * time.Millisecond):
		}
	}
	log.Fatal("voice media timeout")

reconnect:
	old := a
	a2 := mk(tokenA)
	time.Sleep(500 * time.Millisecond)
	_ = old.ws.Close(websocket.StatusNormalClosure, "")
	_ = old.pc.Close()
	time.Sleep(800 * time.Millisecond)
	parts := fetchParticipants(tokenA, roomID)
	found := false
	for _, id := range parts {
		if id != "" {
			found = true
			break
		}
	}
	if !found {
		log.Fatal("reconnect left room empty — stale Leave removed the new peer")
	}
	log.Println("reconnect kept session, participants=", parts)
	_ = a2.ws.Close(websocket.StatusNormalClosure, "")
	_ = a2.pc.Close()
}

func fetchParticipants(token, roomID string) []string {
	req, err := http.NewRequest(http.MethodGet, "http://localhost:8082/rooms/dm/"+roomID+"?token="+token, nil)
	must(err)
	res, err := http.DefaultClient.Do(req)
	must(err)
	defer res.Body.Close()
	var body struct {
		Participants []string `json:"participants"`
	}
	must(json.NewDecoder(res.Body).Decode(&body))
	return body.Participants
}

func runDecline(rtURL, voiceURL string, a, b *authv1.AuthResponse, roomID string) {
	inboxB := make(chan map[string]any, 32)
	wsB := mustWS(rtURL, b.AccessToken, inboxB)
	defer wsB.Close(websocket.StatusNormalClosure, "")
	waitType(inboxB, "ready", 5*time.Second)
	sendJSON(wsB, map[string]any{"type": "subscribe", "dm_ids": []string{roomID}})

	inboxA := make(chan map[string]any, 32)
	wsVoice := mustWS(voiceURL, a.AccessToken, inboxA)
	defer wsVoice.Close(websocket.StatusNormalClosure, "")
	sendJSON(wsVoice, map[string]any{"type": "join", "room_id": roomID, "kind": "dm"})
	waitType(inboxA, "joined", 8*time.Second)
	waitEvent(inboxB, "voice.call.ring", 10*time.Second)
	sendJSON(wsB, map[string]any{"type": "call_decline", "channel_id": roomID})
	waitType(inboxA, "declined", 10*time.Second)
}

func pumpVoice(ctx context.Context, s *voiceSide) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-s.inbox:
			switch msg["type"] {
			case "answer":
				sdp, _ := msg["sdp"].(string)
				_ = s.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
			case "offer":
				sdp, _ := msg["sdp"].(string)
				if s.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}) != nil {
					continue
				}
				ans, err := s.pc.CreateAnswer(nil)
				if err != nil {
					continue
				}
				_ = s.pc.SetLocalDescription(ans)
				sendJSON(s.ws, map[string]any{"type": "answer", "sdp": ans.SDP})
			case "candidate":
				raw, _ := json.Marshal(msg["candidate"])
				var c struct {
					Candidate     string  `json:"candidate"`
					SDPMid        *string `json:"sdpMid"`
					SDPMLineIndex *uint16 `json:"sdpMLineIndex"`
				}
				_ = json.Unmarshal(raw, &c)
				_ = s.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: c.SDPMLineIndex})
			}
		}
	}
}

func voiceOffer(s *voiceSide) error {
	desc, err := s.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := s.pc.SetLocalDescription(desc); err != nil {
		return err
	}
	sendJSON(s.ws, map[string]any{"type": "offer", "sdp": desc.SDP})
	return nil
}

func grpcWeb(api, service, method, token string, req, resp proto.Message) error {
	body, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	frame := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(body)))
	copy(frame[5:], body)
	httpReq, err := http.NewRequest(http.MethodPost, strings.TrimRight(api, "/")+"/"+service+"/"+method, bytes.NewReader(frame))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/grpc-web+proto")
	httpReq.Header.Set("X-Grpc-Web", "1")
	httpReq.Header.Set("Accept", "application/grpc-web+proto")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("%s/%s http %d: %s", service, method, res.StatusCode, string(raw))
	}
	if st := res.Header.Get("Grpc-Status"); st != "" && st != "0" {
		return fmt.Errorf("%s/%s grpc %s: %s", service, method, st, res.Header.Get("Grpc-Message"))
	}
	msgBytes, trailers, err := readGRPCWeb(raw)
	if err != nil {
		return err
	}
	if st := trailers["grpc-status"]; st != "" && st != "0" {
		return fmt.Errorf("%s/%s grpc %s: %s", service, method, st, trailers["grpc-message"])
	}
	return proto.Unmarshal(msgBytes, resp)
}

func readGRPCWeb(raw []byte) ([]byte, map[string]string, error) {
	trailers := map[string]string{}
	var msg []byte
	off := 0
	for off+5 <= len(raw) {
		flag := raw[off]
		n := int(binary.BigEndian.Uint32(raw[off+1 : off+5]))
		off += 5
		if off+n > len(raw) {
			return nil, nil, fmt.Errorf("truncated frame")
		}
		chunk := raw[off : off+n]
		off += n
		if flag&0x80 != 0 {
			for _, line := range strings.Split(string(chunk), "\r\n") {
				if line == "" {
					continue
				}
				k, v, ok := strings.Cut(line, ":")
				if ok {
					trailers[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
				}
			}
			continue
		}
		msg = chunk
	}
	if msg == nil {
		return nil, trailers, fmt.Errorf("no data frame")
	}
	return msg, trailers, nil
}

func mustWS(url, token string, inbox chan map[string]any) *websocket.Conn {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		Subprotocols: []string{"rokko.v1", token},
	})
	must(err)
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			inbox <- msg
		}
	}()
	return conn
}

func sendJSON(conn *websocket.Conn, v any) {
	raw, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(conn.Write(ctx, websocket.MessageText, raw))
}

func waitType(inbox <-chan map[string]any, typ string, d time.Duration) map[string]any {
	deadline := time.After(d)
	for {
		select {
		case msg := <-inbox:
			if msg["type"] == typ {
				return msg
			}
		case <-deadline:
			log.Fatalf("timeout waiting type=%s", typ)
		}
	}
}

func waitEvent(inbox <-chan map[string]any, eventType string, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case msg := <-inbox:
			if msg["type"] == eventType {
				return
			}
			if msg["type"] != "event" {
				continue
			}
			if msg["event_type"] == eventType {
				return
			}
			if payload, ok := msg["payload"].(map[string]any); ok {
				if payload["type"] == eventType || payload["event_type"] == eventType {
					return
				}
			}
		case <-deadline:
			log.Fatalf("timeout waiting event=%s", eventType)
		}
	}
}

func assertNoEvent(inbox <-chan map[string]any, eventType string, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case msg := <-inbox:
			if msg["type"] != "event" {
				continue
			}
			if msg["event_type"] == eventType {
				log.Fatalf("unexpected event %s", eventType)
			}
		case <-deadline:
			return
		}
	}
}

func drainEvents(inbox <-chan map[string]any, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case <-inbox:
		case <-deadline:
			return
		}
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
