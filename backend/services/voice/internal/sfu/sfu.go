package sfu

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

type APIOptions struct {
	UDPPort int
	HostIP  string
}

type Signal func(userID string, payload []byte)

type Message struct {
	Type      string        `json:"type"`
	SDP       string        `json:"sdp,omitempty"`
	Candidate *ICECandidate `json:"candidate,omitempty"`
	UserID    string        `json:"user_id,omitempty"`
	Track     string        `json:"track,omitempty"`
	Layer     string        `json:"layer,omitempty"`
	Paused    bool          `json:"paused,omitempty"`
	BWE       int           `json:"bwe,omitempty"`
}

type ICECandidate struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid"`
	SDPMLineIndex *uint16 `json:"sdpMLineIndex"`
}

type Room struct {
	mu                 sync.Mutex
	id                 string
	api                *webrtc.API
	ice                []webrtc.ICEServer
	peers              map[string]*peer
	sources            map[string]*simulcastSource
	maxParticipants    int
	maxVideoPublishers int
	onError            func(string, error)
	bytesIn            atomic.Uint64
	bytesOut           atomic.Uint64
	layerSwitches      atomic.Uint64
	pli                atomic.Uint64
}

type peer struct {
	userID    string
	pc        *webrtc.PeerConnection
	send      Signal
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	pending   bool
	published []*webrtc.TrackLocalStaticRTP
	bwe       int
	speaking  bool
	videoPubs int
}

func NewAPI(opts APIOptions) (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, registry); err != nil {
		return nil, err
	}
	se := webrtc.SettingEngine{}
	if opts.UDPPort > 0 {
		mux, err := ice.NewMultiUDPMuxFromPort(opts.UDPPort)
		if err != nil {
			return nil, err
		}
		se.SetICEUDPMux(mux)
	}
	if opts.HostIP != "" {
		se.SetNAT1To1IPs([]string{opts.HostIP}, webrtc.ICECandidateTypeHost)
	}
	return webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(registry),
		webrtc.WithSettingEngine(se),
	), nil
}

func NewRoom(id string, api *webrtc.API, iceServers []webrtc.ICEServer) *Room {
	if api == nil {
		built, err := NewAPI(APIOptions{})
		if err != nil || built == nil {
			api = webrtc.NewAPI()
		} else {
			api = built
		}
	}
	return &Room{
		id:                 id,
		api:                api,
		ice:                iceServers,
		peers:              map[string]*peer{},
		sources:            map[string]*simulcastSource{},
		maxParticipants:    25,
		maxVideoPublishers: 9,
	}
}

func (r *Room) SetLimits(participants, video int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if participants > 0 {
		r.maxParticipants = participants
	}
	if video > 0 {
		r.maxVideoPublishers = video
	}
}

func (r *Room) OnError(fn func(string, error)) {
	r.mu.Lock()
	r.onError = fn
	r.mu.Unlock()
}

func (r *Room) report(msg string, err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	fn := r.onError
	r.mu.Unlock()
	if fn != nil {
		fn(msg, err)
	}
}

func (r *Room) ID() string { return r.id }

func (r *Room) Peers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.peers))
	for id := range r.peers {
		out = append(out, id)
	}
	return out
}

func (r *Room) Empty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers) == 0
}

func (r *Room) Stats() RoomStats {
	r.mu.Lock()
	peers := len(r.peers)
	tracks := 0
	bwe := 0
	n := 0
	for _, p := range r.peers {
		tracks += len(p.published)
		p.mu.Lock()
		bwe += p.bwe
		if p.bwe > 0 {
			n++
		}
		p.mu.Unlock()
	}
	r.mu.Unlock()
	avg := 0
	if n > 0 {
		avg = bwe / n
	}
	return RoomStats{
		Peers:         peers,
		Tracks:        tracks,
		BytesIn:       r.bytesIn.Load(),
		BytesOut:      r.bytesOut.Load(),
		LayerSwitches: r.layerSwitches.Load(),
		PLI:           r.pli.Load(),
		BWE:           avg,
	}
}

type RoomStats struct {
	Peers         int
	Tracks        int
	BytesIn       uint64
	BytesOut      uint64
	LayerSwitches uint64
	PLI           uint64
	BWE           int
}

func (r *Room) Join(userID string, send Signal) error {
	r.mu.Lock()
	if _, exists := r.peers[userID]; !exists && r.maxParticipants > 0 && len(r.peers) >= r.maxParticipants {
		r.mu.Unlock()
		return errRoomFull
	}
	r.mu.Unlock()
	pc, err := r.api.NewPeerConnection(webrtc.Configuration{ICEServers: r.ice})
	if err != nil {
		return err
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		_ = pc.Close()
		return err
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		_ = pc.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &peer{userID: userID, pc: pc, send: send, ctx: ctx, cancel: cancel, bwe: 1_500_000}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		p.write(Message{Type: "candidate", Candidate: &ICECandidate{
			Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex,
		}})
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.RID() != "" && track.Kind() == webrtc.RTPCodecTypeVideo {
			r.attachSimulcast(userID, p, track)
			return
		}
		local, err := webrtc.NewTrackLocalStaticRTP(track.Codec().RTPCodecCapability, track.ID(), track.StreamID())
		if err != nil {
			r.report("track", err)
			return
		}
		if track.Kind() == webrtc.RTPCodecTypeVideo && !r.allowVideo(p) {
			p.write(Message{Type: "error", SDP: "video_limit"})
			return
		}
		r.forward(userID, local)
		go pump(p.ctx, track, local, &r.bytesIn, &r.bytesOut)
	})
	r.mu.Lock()
	if old := r.peers[userID]; old != nil {
		old.cancel()
		_ = old.pc.Close()
	}
	r.peers[userID] = p
	others := make([]string, 0, len(r.peers)-1)
	for id := range r.peers {
		if id != userID {
			others = append(others, id)
		}
	}
	r.mu.Unlock()
	p.write(Message{Type: "joined", UserID: userID})
	for _, other := range others {
		p.write(Message{Type: "peer_joined", UserID: other})
		r.writeTo(other, Message{Type: "peer_joined", UserID: userID})
	}
	return nil
}

func (r *Room) allowVideo(p *peer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.videoPubs > 0 {
		return true
	}
	if r.maxVideoPublishers <= 0 {
		p.videoPubs++
		return true
	}
	count := 0
	for _, other := range r.peers {
		count += other.videoPubs
	}
	if count >= r.maxVideoPublishers {
		return false
	}
	p.videoPubs++
	return true
}

func (r *Room) Signal(userID string, raw []byte) error {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	r.mu.Lock()
	p := r.peers[userID]
	r.mu.Unlock()
	if p == nil {
		return nil
	}
	switch msg.Type {
	case "offer":
		r.attachExisting(p)
		if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}); err != nil {
			return err
		}
		answer, err := p.pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		if err := p.pc.SetLocalDescription(answer); err != nil {
			return err
		}
		p.write(Message{Type: "answer", SDP: answer.SDP})
	case "answer":
		if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: msg.SDP}); err != nil {
			return err
		}
		p.mu.Lock()
		pending := p.pending
		p.pending = false
		p.mu.Unlock()
		if pending {
			if err := p.negotiate(); err != nil {
				r.report("negotiate", err)
				return err
			}
		}
	case "candidate":
		if msg.Candidate == nil || msg.Candidate.Candidate == "" {
			return nil
		}
		return p.pc.AddICECandidate(webrtc.ICECandidateInit{
			Candidate: msg.Candidate.Candidate, SDPMid: msg.Candidate.SDPMid, SDPMLineIndex: msg.Candidate.SDPMLineIndex,
		})
	case "set_layer":
		r.setPreferred(userID, msg.Track, Layer(msg.Layer))
	case "pause_video":
		r.pauseSource(userID, msg.Track, msg.Paused)
	case "bwe":
		if msg.BWE > 0 {
			p.mu.Lock()
			p.bwe = msg.BWE
			p.mu.Unlock()
			r.retarget(userID)
		}
	}
	return nil
}

func (r *Room) SetBandwidth(userID string, bps int) {
	_ = r.Signal(userID, mustJSON(Message{Type: "bwe", BWE: bps}))
}

func (r *Room) Leave(userID string) bool {
	r.mu.Lock()
	p := r.peers[userID]
	delete(r.peers, userID)
	others := make([]*peer, 0, len(r.peers))
	for _, other := range r.peers {
		others = append(others, other)
	}
	var published []*webrtc.TrackLocalStaticRTP
	if p != nil {
		published = append(published, p.published...)
	}
	empty := len(r.peers) == 0
	r.dropSourcesLocked(userID)
	r.mu.Unlock()
	if p != nil {
		p.cancel()
		_ = p.pc.Close()
	}
	for _, other := range others {
		if other.removeTracks(published) {
			if err := other.negotiate(); err != nil {
				r.report("leave negotiate", err)
			}
		}
	}
	r.broadcast(userID, Message{Type: "peer_left", UserID: userID})
	return empty
}

func (r *Room) Kick(userID, reason string) bool {
	r.writeTo(userID, Message{Type: reason, UserID: userID})
	return r.Leave(userID)
}

func (p *peer) removeTracks(tracks []*webrtc.TrackLocalStaticRTP) bool {
	removed := false
	for _, sender := range p.pc.GetSenders() {
		for _, track := range tracks {
			if sender.Track() == track && p.pc.RemoveTrack(sender) == nil {
				removed = true
			}
		}
	}
	return removed
}

func (r *Room) attachExisting(p *peer) {
	r.mu.Lock()
	var tracks []*webrtc.TrackLocalStaticRTP
	for id, other := range r.peers {
		if id != p.userID {
			tracks = append(tracks, other.published...)
		}
	}
	sources := make([]*simulcastSource, 0, len(r.sources))
	for _, src := range r.sources {
		if src.from != p.userID {
			sources = append(sources, src)
		}
	}
	r.mu.Unlock()
	for _, track := range tracks {
		if p.hasTrack(track) {
			continue
		}
		if _, err := p.pc.AddTrack(track); err != nil {
			r.report("attach", err)
		}
	}
	for _, src := range sources {
		r.ensureForwarder(p, src)
	}
}

func (p *peer) hasTrack(local *webrtc.TrackLocalStaticRTP) bool {
	for _, sender := range p.pc.GetSenders() {
		if sender.Track() == local {
			return true
		}
	}
	return false
}

func (r *Room) forward(from string, local *webrtc.TrackLocalStaticRTP) {
	r.mu.Lock()
	if src := r.peers[from]; src != nil {
		src.published = append(src.published, local)
	}
	var targets []*peer
	for id, p := range r.peers {
		if id != from {
			targets = append(targets, p)
		}
	}
	r.mu.Unlock()
	for _, p := range targets {
		if _, err := p.pc.AddTrack(local); err != nil {
			r.report("forward", err)
			continue
		}
		if err := p.negotiate(); err != nil {
			r.report("forward negotiate", err)
		}
	}
}

func (p *peer) negotiate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.pending = true
		return nil
	}
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return err
	}
	p.write(Message{Type: "offer", SDP: offer.SDP})
	return nil
}

func (p *peer) write(msg Message) {
	if p.send == nil {
		return
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	p.send(p.userID, raw)
}

func (r *Room) writeTo(userID string, msg Message) {
	r.mu.Lock()
	p := r.peers[userID]
	r.mu.Unlock()
	if p != nil {
		p.write(msg)
	}
}

func (r *Room) broadcast(except string, msg Message) {
	r.mu.Lock()
	peers := make([]*peer, 0, len(r.peers))
	for id, p := range r.peers {
		if id != except {
			peers = append(peers, p)
		}
	}
	r.mu.Unlock()
	for _, p := range peers {
		p.write(msg)
	}
}

func pump(ctx context.Context, remote *webrtc.TrackRemote, local *webrtc.TrackLocalStaticRTP, in, out *atomic.Uint64) {
	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return
		}
		_ = remote.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := remote.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if in != nil {
			in.Add(uint64(n))
		}
		if _, err := local.Write(buf[:n]); err != nil {
			return
		}
		if out != nil {
			out.Add(uint64(n))
		}
	}
}

func mustJSON(msg Message) []byte {
	raw, _ := json.Marshal(msg)
	return raw
}
