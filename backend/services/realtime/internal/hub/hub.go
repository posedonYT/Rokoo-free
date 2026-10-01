package hub

import "sync"

type Conn interface {
	UserID() string
	Send(payload []byte) bool
	Close()
}

type Hub struct {
	mu    sync.RWMutex
	conns map[string]map[Conn]struct{}
}

func New() *Hub { return &Hub{conns: map[string]map[Conn]struct{}{}} }

func (h *Hub) Add(c Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.conns[c.UserID()]
	if set == nil {
		set = map[Conn]struct{}{}
		h.conns[c.UserID()] = set
	}
	set[c] = struct{}{}
}

func (h *Hub) Remove(c Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.conns[c.UserID()]
	delete(set, c)
	if len(set) == 0 {
		delete(h.conns, c.UserID())
	}
}

func (h *Hub) Send(userID string, payload []byte) bool {
	var dropped []Conn
	h.mu.RLock()
	for c := range h.conns[userID] {
		if !c.Send(payload) {
			dropped = append(dropped, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range dropped {
		h.Remove(c)
		c.Close()
	}
	return len(dropped) == 0
}

func (h *Hub) Online(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[userID]) > 0
}
