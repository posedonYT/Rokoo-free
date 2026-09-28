package repository

import (
	"context"
	"strings"
	"sync"

	"rokko.dev/pkg/apperr"
	"rokko.dev/services/user/internal/domain"
)

type Memory struct {
	mu    sync.Mutex
	users map[int64]domain.Profile
	rels  map[[2]int64]string
}

func NewMemory() *Memory {
	return &Memory{users: map[int64]domain.Profile{}, rels: map[[2]int64]string{}}
}

func (m *Memory) InTx(_ context.Context, fn func(Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(m)
}

func (m *Memory) InsertProfile(_ context.Context, p domain.Profile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[p.ID]; ok {
		return nil
	}
	for _, existing := range m.users {
		if existing.Username == p.Username && existing.Discriminator == p.Discriminator {
			return apperr.Conflict("discriminator taken")
		}
	}
	m.users[p.ID] = p
	return nil
}

func (m *Memory) UpdateProfile(_ context.Context, p domain.Profile) (domain.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.users[p.ID]
	if !ok {
		return domain.Profile{}, apperr.NotFound("profile not found")
	}
	cur.DisplayName = p.DisplayName
	cur.AvatarURL = p.AvatarURL
	cur.StatusText = p.StatusText
	m.users[p.ID] = cur
	return cur, nil
}

func (m *Memory) GetProfile(_ context.Context, id int64) (domain.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.users[id]
	if !ok {
		return domain.Profile{}, apperr.NotFound("profile not found")
	}
	return p, nil
}

func (m *Memory) Search(_ context.Context, query string, limit int) ([]domain.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := strings.ToLower(query)
	var out []domain.Profile
	for _, p := range m.users {
		tag := p.Username + "#" + p.Discriminator
		if strings.HasPrefix(p.Username, q) || tag == q {
			out = append(out, p)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *Memory) ListByRel(_ context.Context, userID int64, relType string) ([]domain.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Profile
	for key, typ := range m.rels {
		if key[0] == userID && typ == relType {
			if p, ok := m.users[key[1]]; ok {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func (m *Memory) FriendIDs(_ context.Context, userID int64) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []int64
	for key, typ := range m.rels {
		if key[0] == userID && typ == domain.RelFriend {
			out = append(out, key[1])
		}
	}
	return out, nil
}

func (m *Memory) IsBlocked(_ context.Context, a, b int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rels[[2]int64{a, b}] == domain.RelBlocked || m.rels[[2]int64{b, a}] == domain.RelBlocked, nil
}

func (m *Memory) GetRel(_ context.Context, userID, targetID int64) (string, error) {
	return m.rels[[2]int64{userID, targetID}], nil
}

func (m *Memory) PutRel(_ context.Context, userID, targetID int64, relType string) error {
	m.rels[[2]int64{userID, targetID}] = relType
	return nil
}

func (m *Memory) DelRel(_ context.Context, userID, targetID int64) error {
	delete(m.rels, [2]int64{userID, targetID})
	return nil
}

func (m *Memory) AddEvent(context.Context, domain.Event) error { return nil }
