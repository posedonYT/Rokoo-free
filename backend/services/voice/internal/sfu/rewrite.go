package sfu

import "github.com/pion/rtp"

type rewriter struct {
	outSeq   uint16
	outTS    uint32
	lastSeq  uint16
	lastTS   uint32
	started  bool
	switched bool
}

func (r *rewriter) Rewrite(pkt *rtp.Packet, switched bool) {
	if !r.started {
		r.outSeq = pkt.SequenceNumber
		r.outTS = pkt.Timestamp
		r.lastSeq = pkt.SequenceNumber
		r.lastTS = pkt.Timestamp
		r.started = true
		pkt.SequenceNumber = r.outSeq
		pkt.Timestamp = r.outTS
		return
	}
	if switched {
		r.outSeq++
		deltaTS := pkt.Timestamp - r.lastTS
		if deltaTS == 0 {
			deltaTS = 3000
		}
		r.outTS += deltaTS
		r.lastSeq = pkt.SequenceNumber
		r.lastTS = pkt.Timestamp
		pkt.SequenceNumber = r.outSeq
		pkt.Timestamp = r.outTS
		return
	}
	dSeq := pkt.SequenceNumber - r.lastSeq
	dTS := pkt.Timestamp - r.lastTS
	r.outSeq += dSeq
	r.outTS += dTS
	r.lastSeq = pkt.SequenceNumber
	r.lastTS = pkt.Timestamp
	pkt.SequenceNumber = r.outSeq
	pkt.Timestamp = r.outTS
}
