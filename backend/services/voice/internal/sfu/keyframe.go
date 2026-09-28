package sfu

import "github.com/pion/rtp"

func isKeyframe(pkt *rtp.Packet) bool {
	if pkt == nil || len(pkt.Payload) == 0 {
		return false
	}
	return pkt.Payload[0]&0x01 == 0
}
