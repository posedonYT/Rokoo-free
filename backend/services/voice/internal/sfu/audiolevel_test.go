package sfu

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

func TestAudioLevel(t *testing.T) {
	pkt := &rtp.Packet{Payload: []byte{1}}
	require.NoError(t, pkt.SetExtension(1, []byte{0x80 | 12}))
	level, ok := audioLevel(pkt)
	require.True(t, ok)
	require.Equal(t, uint8(12), level)
	require.True(t, speaking(level))
	require.False(t, speaking(90))
}
