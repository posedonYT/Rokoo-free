package sfu

import "github.com/pion/rtp"

func audioLevel(pkt *rtp.Packet) (level uint8, ok bool) {
	if pkt == nil {
		return 0, false
	}
	raw := pkt.GetExtension(1)
	if len(raw) == 0 {
		return 0, false
	}
	return raw[0] & 0x7f, true
}

func speaking(level uint8) bool {
	return level <= 40
}
