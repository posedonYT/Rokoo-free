package repository

import (
	"context"
	"sort"
	"sync"

	"rokko.dev/pkg/apperr"
	"rokko.dev/services/message/internal/domain"
)

type Memory struct {
	mu       sync.Mutex
	dms      map[int64]domain.DMChannel
	pairs    map[[2]int64]int64
	messages map[int64]domain.Message
	deleted  map[int64]bool
}

func NewMemory() *Memory {
	return &Memory{
		dms:      map[int64]domain.DMChannel{},
		pairs:    map[[2]int64]int64{},
		messages: map[int64]domain.Message{},
		deleted:  map[int64]bool{},
	}
}

func (m *Memory) FindPair(_ context.Context, a, b int64) (domain.DMChannel, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lo, hi := order(a, b)
	id, ok := m.pairs[[2]int64{lo, hi}]
	if !ok {
		return domain.DMChannel{}, false, nil
	}
	return m.dms[id], true, nil
}

func (m *Memory) CreateDM(_ context.Context, ch domain.DMChannel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dms[ch.ID] = cloneDM(ch)
	if ch.Kind == domain.KindDM && len(ch.Members) == 2 {
		lo, hi := order(ch.Members[0], ch.Members[1])
		m.pairs[[2]int64{lo, hi}] = ch.ID
	}
	return nil
}

func (m *Memory) DM(_ context.Context, channelID int64) (domain.DMChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.dms[channelID]
	if !ok {
		return domain.DMChannel{}, apperr.NotFound("dm not found")
	}
	return cloneDM(ch), nil
}

func (m *Memory) ListDMs(_ context.Context, userID int64) ([]domain.DMChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.DMChannel
	for _, ch := range m.dms {
		if contains(ch.Members, userID) {
			out = append(out, cloneDM(ch))
		}
	}
	return out, nil
}

func (m *Memory) AddMember(_ context.Context, channelID, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.dms[channelID]
	if !ok {
		return apperr.NotFound("dm not found")
	}
	if !contains(ch.Members, userID) {
		ch.Members = append(ch.Members, userID)
		m.dms[channelID] = ch
	}
	return nil
}

func (m *Memory) RemoveMember(_ context.Context, channelID, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.dms[channelID]
	if !ok {
		return apperr.NotFound("dm not found")
	}
	next := ch.Members[:0]
	for _, id := range ch.Members {
		if id != userID {
			next = append(next, id)
		}
	}
	ch.Members = next
	m.dms[channelID] = ch
	return nil
}

func (m *Memory) InsertMessage(_ context.Context, msg domain.Message, _ domain.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.ID] = msg
	return nil
}

func (m *Memory) UpdateMessage(_ context.Context, msg domain.Message, _ domain.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.messages[msg.ID]
	if !ok || m.deleted[msg.ID] {
		return apperr.NotFound("message not found")
	}
	cur.Content = msg.Content
	cur.EditedAt = msg.EditedAt
	m.messages[msg.ID] = cur
	return nil
}

func (m *Memory) SoftDelete(_ context.Context, messageID int64, _ domain.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.messages[messageID]; !ok || m.deleted[messageID] {
		return apperr.NotFound("message not found")
	}
	m.deleted[messageID] = true
	return nil
}

func (m *Memory) GetMessage(_ context.Context, messageID int64) (domain.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[messageID]
	if !ok || m.deleted[messageID] {
		return domain.Message{}, apperr.NotFound("message not found")
	}
	return msg, nil
}

func (m *Memory) ListMessages(_ context.Context, channelID int64, before int64, limit int) ([]domain.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Message
	for _, msg := range m.messages {
		if msg.ChannelID != channelID || m.deleted[msg.ID] {
			continue
		}
		if before > 0 && msg.ID >= before {
			continue
		}
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func order(a, b int64) (int64, int64) {
	if a < b {
		return a, b
	}
	return b, a
}

func contains(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func cloneDM(ch domain.DMChannel) domain.DMChannel {
	cp := ch
	cp.Members = append([]int64(nil), ch.Members...)
	return cp
}
