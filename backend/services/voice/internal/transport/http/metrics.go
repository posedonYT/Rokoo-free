package httpserver

import "github.com/prometheus/client_golang/prometheus"

type metrics struct {
	reg      *prometheus.Registry
	rooms    prometheus.Gauge
	peers    prometheus.Gauge
	tracks   prometheus.Gauge
	bytesIn  prometheus.Gauge
	bytesOut prometheus.Gauge
	switches prometheus.Gauge
	pli      prometheus.Gauge
	bwe      prometheus.Gauge
	queue    prometheus.Gauge
}

func newMetrics(reg *prometheus.Registry) *metrics {
	m := &metrics{reg: reg}
	g := func(name, help string) prometheus.Gauge {
		c := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
		reg.MustRegister(c)
		return c
	}
	m.rooms = g("voice_rooms", "Active voice rooms")
	m.peers = g("voice_peers", "Connected peers")
	m.tracks = g("voice_tracks", "Published tracks")
	m.bytesIn = g("voice_bytes_in", "RTP bytes received")
	m.bytesOut = g("voice_bytes_out", "RTP bytes sent")
	m.switches = g("voice_layer_switches", "Simulcast layer switches")
	m.pli = g("voice_pli", "PLI requests")
	m.bwe = g("voice_bwe_bps", "Highest peer bandwidth estimate")
	m.queue = g("voice_write_queue", "Signaling write queue depth")
	return m
}
