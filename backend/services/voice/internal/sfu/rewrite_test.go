package sfu

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

func TestRewriterWrap(t *testing.T) {
	var rw rewriter
	first := &rtp.Packet{Header: rtp.Header{SequenceNumber: 65534, Timestamp: 4294967000}, Payload: []byte{1}}
	rw.Rewrite(first, false)
	require.Equal(t, uint16(65534), first.SequenceNumber)

	second := &rtp.Packet{Header: rtp.Header{SequenceNumber: 65535, Timestamp: 100}, Payload: []byte{1}}
	rw.Rewrite(second, false)
	require.Equal(t, uint16(65535), second.SequenceNumber)
	require.Equal(t, uint32(100), second.Timestamp)

	third := &rtp.Packet{Header: rtp.Header{SequenceNumber: 0, Timestamp: 400}, Payload: []byte{1}}
	rw.Rewrite(third, false)
	require.Equal(t, uint16(0), third.SequenceNumber)
	require.Equal(t, uint32(400), third.Timestamp)

	switched := &rtp.Packet{Header: rtp.Header{SequenceNumber: 50, Timestamp: 9000}, Payload: []byte{1}}
	rw.Rewrite(switched, true)
	require.Equal(t, uint16(1), switched.SequenceNumber)
}
