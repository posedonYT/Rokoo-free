package sfu

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/stretchr/testify/require"
)

func TestTwoPeersExchangeMedia(t *testing.T) {
	room := NewRoom("room", nil, nil)
	var mu sync.Mutex
	inbox := map[string][][]byte{}
	send := func(userID string, payload []byte) {
		mu.Lock()
		inbox[userID] = append(inbox[userID], append([]byte(nil), payload...))
		mu.Unlock()
	}
	require.NoError(t, room.Join("a", send))
	require.NoError(t, room.Join("b", send))
	require.ElementsMatch(t, []string{"a", "b"}, room.Peers())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	left := newClient(t)
	right := newClient(t)
	startPump(ctx, room, "a", left, &mu, inbox)
	startPump(ctx, room, "b", right, &mu, inbox)
	require.NoError(t, offer(left, room, "a"))
	require.NoError(t, offer(right, room, "b"))

	waitState(t, left, webrtc.PeerConnectionStateConnected)
	waitState(t, right, webrtc.PeerConnectionStateConnected)

	got := make(chan struct{}, 1)
	right.OnTrack(func(*webrtc.TrackRemote, *webrtc.RTPReceiver) {
		select {
		case got <- struct{}{}:
		default:
		}
	})
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", "stream")
	require.NoError(t, err)
	_, err = left.AddTrack(track)
	require.NoError(t, err)
	require.NoError(t, offer(left, room, "a"))

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = track.WriteSample(media.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond})
		select {
		case <-got:
			room.mu.Lock()
			b := room.peers["b"]
			room.mu.Unlock()
			require.True(t, hasActiveSender(b))
			room.Leave("a")
			require.False(t, hasActiveSender(b))
			require.Equal(t, []string{"b"}, room.Peers())
			room.Leave("b")
			return
		case <-time.After(40 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for forwarded track")
}

func newClient(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

func startPump(ctx context.Context, room *Room, userID string, pc *webrtc.PeerConnection, mu *sync.Mutex, inbox map[string][][]byte) {
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		_ = room.Signal(userID, mustMsg(Message{Type: "candidate", Candidate: &ICECandidate{
			Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex,
		}}))
	})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			mu.Lock()
			batch := append([][]byte(nil), inbox[userID]...)
			inbox[userID] = nil
			mu.Unlock()
			for _, raw := range batch {
				var msg Message
				if jsonUnmarshal(raw, &msg) != nil {
					continue
				}
				switch msg.Type {
				case "answer":
					_ = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: msg.SDP})
				case "offer":
					if pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}) != nil {
						continue
					}
					answer, err := pc.CreateAnswer(nil)
					if err != nil {
						continue
					}
					_ = pc.SetLocalDescription(answer)
					_ = room.Signal(userID, mustMsg(Message{Type: "answer", SDP: answer.SDP}))
				case "candidate":
					if msg.Candidate != nil {
						_ = pc.AddICECandidate(webrtc.ICECandidateInit{
							Candidate: msg.Candidate.Candidate, SDPMid: msg.Candidate.SDPMid, SDPMLineIndex: msg.Candidate.SDPMLineIndex,
						})
					}
				}
			}
			time.Sleep(15 * time.Millisecond)
		}
	}()
}

func offer(pc *webrtc.PeerConnection, room *Room, userID string) error {
	desc, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(desc); err != nil {
		return err
	}
	return room.Signal(userID, mustMsg(Message{Type: "offer", SDP: desc.SDP}))
}

func mustMsg(msg Message) []byte {
	raw, _ := jsonMarshal(msg)
	return raw
}

func waitState(t *testing.T, pc *webrtc.PeerConnection, want webrtc.PeerConnectionState) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if pc.ConnectionState() == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state %s", pc.ConnectionState())
}

func hasActiveSender(p *peer) bool {
	for _, sender := range p.pc.GetSenders() {
		if sender.Track() != nil {
			return true
		}
	}
	return false
}
