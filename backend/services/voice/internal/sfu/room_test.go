package sfu

import (
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

func TestLeaveReportsEmptyAndGC(t *testing.T) {
	room := NewRoom("r", nil, nil)
	sess, err := room.Join("a", func(string, []byte) {})
	require.NoError(t, err)
	require.True(t, room.Leave(sess))
	require.True(t, room.Empty())
	require.True(t, room.ForceLeave("missing"))
}

func TestRoomFull(t *testing.T) {
	room := NewRoom("r", nil, nil)
	room.SetLimits(1, 1)
	_, err := room.Join("a", func(string, []byte) {})
	require.NoError(t, err)
	_, err = room.Join("b", func(string, []byte) {})
	require.Error(t, err)
	require.True(t, IsRoomFull(err))
}

func TestConcurrentJoinLeave(t *testing.T) {
	room := NewRoom("r", nil, nil)
	room.SetLimits(8, 4)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := string(rune('a' + n))
			sess, err := room.Join(id, func(string, []byte) {})
			if err != nil {
				return
			}
			room.Leave(sess)
		}(i)
	}
	wg.Wait()
	require.True(t, room.Empty())
}

func TestLeaveIgnoresStaleSession(t *testing.T) {
	room := NewRoom("r", nil, nil)
	old, err := room.Join("a", func(string, []byte) {})
	require.NoError(t, err)
	fresh, err := room.Join("a", func(string, []byte) {})
	require.NoError(t, err)
	require.False(t, room.Leave(old))
	require.Equal(t, []string{"a"}, room.Peers())
	require.True(t, room.Leave(fresh))
	require.True(t, room.Empty())
}

func TestFanoutSetLayerNoDeadlock(t *testing.T) {
	room := NewRoom("r", nil, nil)
	a, err := room.Join("a", func(string, []byte) {})
	require.NoError(t, err)
	b, err := room.Join("b", func(string, []byte) {})
	require.NoError(t, err)
	defer room.Leave(a)
	defer room.Leave(b)

	local, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "t", "s")
	require.NoError(t, err)

	real := &simulcastSource{
		id: "a:stream", from: "a",
		layers: map[Layer]*webrtc.TrackRemote{LayerH: nil, LayerF: nil},
		outs: map[string]*subForwarder{
			"b": {peer: b.peer, local: local, preferred: LayerH, current: LayerNone},
		},
		paused: map[string]bool{},
		pliAt:  map[Layer]time.Time{},
	}
	room.mu.Lock()
	room.sources[real.id] = real
	room.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pkt := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 1, SSRC: 1}}
		for i := 0; i < 200; i++ {
			room.fanout(real, LayerH, pkt, i%2 == 0)
			room.setPreferred("b", real.id, LayerF)
			room.retarget("b")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock in fanout/setPreferred")
	}
}
