package sfu

import (
	"context"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type simulcastSource struct {
	id     string
	from   string
	layers map[Layer]*webrtc.TrackRemote
	outs   map[string]*subForwarder
	paused map[string]bool
	mu     sync.Mutex
}

type subForwarder struct {
	peer      *peer
	local     *webrtc.TrackLocalStaticRTP
	rewrite   rewriter
	current   Layer
	preferred Layer
	paused    bool
}

func (r *Room) attachSimulcast(from string, publisher *peer, track *webrtc.TrackRemote) {
	if !r.allowVideo(publisher) {
		publisher.write(Message{Type: "error", SDP: "video_limit"})
		return
	}
	layer := Layer(track.RID())
	if layer != LayerQ && layer != LayerH && layer != LayerF {
		layer = LayerF
	}
	key := from + ":" + track.StreamID()
	r.mu.Lock()
	src := r.sources[key]
	if src == nil {
		src = &simulcastSource{
			id: key, from: from,
			layers: map[Layer]*webrtc.TrackRemote{},
			outs:   map[string]*subForwarder{},
			paused: map[string]bool{},
		}
		r.sources[key] = src
	}
	src.mu.Lock()
	src.layers[layer] = track
	var targets []*peer
	for id, p := range r.peers {
		if id != from {
			targets = append(targets, p)
		}
	}
	src.mu.Unlock()
	r.mu.Unlock()
	for _, p := range targets {
		r.ensureForwarder(p, src)
	}
	go r.readLayer(publisher.ctx, src, layer, track)
}

func (r *Room) ensureForwarder(p *peer, src *simulcastSource) {
	src.mu.Lock()
	if _, ok := src.outs[p.userID]; ok {
		src.mu.Unlock()
		return
	}
	local, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, src.id, src.id)
	if err != nil {
		src.mu.Unlock()
		r.report("simulcast track", err)
		return
	}
	fwd := &subForwarder{peer: p, local: local, preferred: LayerH, current: LayerNone}
	src.outs[p.userID] = fwd
	src.mu.Unlock()
	if _, err := p.pc.AddTrack(local); err != nil {
		r.report("simulcast add", err)
		return
	}
	r.mu.Lock()
	if pub := r.peers[p.userID]; pub != nil {
		pub.published = append(pub.published, local)
	}
	r.mu.Unlock()
	if err := p.negotiate(); err != nil {
		r.report("simulcast negotiate", err)
	}
}

func (r *Room) readLayer(ctx context.Context, src *simulcastSource, layer Layer, track *webrtc.TrackRemote) {
	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return
		}
		_ = track.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := track.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		r.bytesIn.Add(uint64(n))
		pkt := &rtp.Packet{}
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		if level, ok := audioLevel(pkt); ok {
			r.markSpeaking(src.from, speaking(level))
		}
		r.fanout(src, layer, pkt, isKeyframe(pkt))
	}
}

func (r *Room) markSpeaking(userID string, on bool) {
	r.mu.Lock()
	p := r.peers[userID]
	r.mu.Unlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	p.speaking = on
	p.mu.Unlock()
	if on {
		r.preferSpeaker(userID)
	}
}

func (r *Room) preferSpeaker(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, src := range r.sources {
		if src.from != userID {
			continue
		}
		src.mu.Lock()
		for _, fwd := range src.outs {
			if fwd.preferred != LayerF {
				fwd.preferred = LayerF
			}
		}
		src.mu.Unlock()
	}
}

func (r *Room) fanout(src *simulcastSource, layer Layer, pkt *rtp.Packet, key bool) {
	src.mu.Lock()
	defer src.mu.Unlock()
	for id, fwd := range src.outs {
		if src.paused[id] || fwd.paused {
			continue
		}
		r.mu.Lock()
		p := r.peers[id]
		r.mu.Unlock()
		bwe := 1_500_000
		if p != nil {
			p.mu.Lock()
			bwe = p.bwe
			p.mu.Unlock()
		}
		available := make([]Layer, 0, len(src.layers))
		for l := range src.layers {
			available = append(available, l)
		}
		want := Choose(bwe, fwd.preferred, available)
		if want == LayerNone || want != layer {
			continue
		}
		switched := fwd.current != want
		if switched && !key {
			r.requestPLI(src, layer)
			continue
		}
		if switched {
			fwd.current = want
			r.layerSwitches.Add(1)
		}
		clone := pkt.Clone()
		fwd.rewrite.Rewrite(clone, switched)
		raw, err := clone.Marshal()
		if err != nil {
			continue
		}
		if _, err := fwd.local.Write(raw); err != nil {
			continue
		}
		r.bytesOut.Add(uint64(len(raw)))
	}
}

func (r *Room) requestPLI(src *simulcastSource, layer Layer) {
	r.pli.Add(1)
	r.mu.Lock()
	pub := r.peers[src.from]
	track := src.layers[layer]
	r.mu.Unlock()
	if pub == nil || track == nil {
		return
	}
	_ = pub.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
}

func (r *Room) setPreferred(userID, track string, layer Layer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, src := range r.sources {
		if track != "" && id != track && src.id != track {
			continue
		}
		src.mu.Lock()
		if fwd := src.outs[userID]; fwd != nil {
			fwd.preferred = layer
		}
		src.mu.Unlock()
	}
}

func (r *Room) pauseSource(userID, track string, paused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, src := range r.sources {
		if track != "" && id != track && src.id != track {
			continue
		}
		src.mu.Lock()
		src.paused[userID] = paused
		if fwd := src.outs[userID]; fwd != nil {
			fwd.paused = paused
		}
		src.mu.Unlock()
	}
}

func (r *Room) retarget(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, src := range r.sources {
		src.mu.Lock()
		if fwd := src.outs[userID]; fwd != nil && fwd.current != LayerNone {
			fwd.current = LayerNone
		}
		src.mu.Unlock()
	}
}

func (r *Room) dropSourcesLocked(userID string) {
	for id, src := range r.sources {
		if src.from == userID {
			delete(r.sources, id)
			continue
		}
		src.mu.Lock()
		delete(src.outs, userID)
		delete(src.paused, userID)
		src.mu.Unlock()
	}
}
