package sfu

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type simulcastSource struct {
	id       string
	from     string
	layers   map[Layer]*webrtc.TrackRemote
	outs     map[string]*subForwarder
	paused   map[string]bool
	pliAt    map[Layer]time.Time
	mu       sync.Mutex
	codec    webrtc.RTPCodecCapability
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
			pliAt:  map[Layer]time.Time{},
			codec:  track.Codec().RTPCodecCapability,
		}
		r.sources[key] = src
	}
	src.mu.Lock()
	src.layers[layer] = track
	if src.codec.MimeType == "" {
		src.codec = track.Codec().RTPCodecCapability
	}
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
	codec := src.codec
	if codec.MimeType == "" {
		codec = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	}
	local, err := webrtc.NewTrackLocalStaticRTP(codec, src.id, src.id)
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
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return
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
	r.mu.Lock()
	bwes := make(map[string]int, len(r.peers))
	pub := r.peers[src.from]
	for id, p := range r.peers {
		p.mu.Lock()
		bwes[id] = p.bwe
		p.mu.Unlock()
	}
	r.mu.Unlock()

	src.mu.Lock()
	defer src.mu.Unlock()
	track := src.layers[layer]
	available := make([]Layer, 0, len(src.layers))
	for l := range src.layers {
		available = append(available, l)
	}
	var pliRequests []Layer
	for id, fwd := range src.outs {
		if src.paused[id] || fwd.paused {
			continue
		}
		bwe := bwes[id]
		if bwe == 0 {
			bwe = 1_500_000
		}
		want := Choose(bwe, fwd.preferred, available)
		if want == LayerNone || want != layer {
			continue
		}
		switched := fwd.current != want
		if switched && !key {
			pliRequests = append(pliRequests, layer)
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
	if len(pliRequests) > 0 && pub != nil && track != nil {
		r.requestPLILocked(src, pub, track, layer)
	}
}

func (r *Room) requestPLILocked(src *simulcastSource, pub *peer, track *webrtc.TrackRemote, layer Layer) {
	now := time.Now()
	if last, ok := src.pliAt[layer]; ok && now.Sub(last) < 500*time.Millisecond {
		return
	}
	src.pliAt[layer] = now
	r.pli.Add(1)
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
