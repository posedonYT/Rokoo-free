package repository

import (
	"context"
	"sort"
	"sync"
	"time"

	"rokko.dev/pkg/apperr"
	"rokko.dev/services/guild/internal/domain"
)

type Memory struct {
	mu       sync.Mutex
	guilds   map[int64]domain.Guild
	channels map[int64]domain.Channel
	members  map[[2]int64]string
	invites  map[string]domain.Invite
}

func NewMemory() *Memory {
	return &Memory{
		guilds:   map[int64]domain.Guild{},
		channels: map[int64]domain.Channel{},
		members:  map[[2]int64]string{},
		invites:  map[string]domain.Invite{},
	}
}

func (m *Memory) CreateGuild(_ context.Context, g domain.Guild, channels []domain.Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.guilds[g.ID] = g
	m.members[[2]int64{g.ID, g.OwnerID}] = domain.RoleOwner
	for _, ch := range channels {
		m.channels[ch.ID] = ch
	}
	return nil
}

func (m *Memory) UpdateGuild(_ context.Context, g domain.Guild) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.guilds[g.ID]
	if !ok {
		return apperr.NotFound("guild not found")
	}
	cur.Name = g.Name
	cur.IconURL = g.IconURL
	m.guilds[g.ID] = cur
	return nil
}

func (m *Memory) DeleteGuild(_ context.Context, guildID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.guilds, guildID)
	for id, ch := range m.channels {
		if ch.GuildID == guildID {
			delete(m.channels, id)
		}
	}
	for key := range m.members {
		if key[0] == guildID {
			delete(m.members, key)
		}
	}
	for code, inv := range m.invites {
		if inv.GuildID == guildID {
			delete(m.invites, code)
		}
	}
	return nil
}

func (m *Memory) GetGuild(_ context.Context, guildID int64) (domain.Guild, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.guilds[guildID]
	if !ok {
		return domain.Guild{}, apperr.NotFound("guild not found")
	}
	return g, nil
}

func (m *Memory) ListGuilds(_ context.Context, userID int64) ([]domain.Guild, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Guild
	for key, role := range m.members {
		if key[1] == userID {
			g := m.guilds[key[0]]
			g.MyRole = role
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *Memory) Role(_ context.Context, guildID, userID int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	role, ok := m.members[[2]int64{guildID, userID}]
	if !ok {
		return "", apperr.Forbidden("not a member")
	}
	return role, nil
}

func (m *Memory) SaveChannel(_ context.Context, ch domain.Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[ch.ID] = ch
	return nil
}

func (m *Memory) DeleteChannel(_ context.Context, channelID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.channels, channelID)
	return nil
}

func (m *Memory) Channel(_ context.Context, channelID int64) (domain.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[channelID]
	if !ok {
		return domain.Channel{}, apperr.NotFound("channel not found")
	}
	return ch, nil
}

func (m *Memory) Channels(_ context.Context, guildID int64) ([]domain.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Channel
	for _, ch := range m.channels {
		if ch.GuildID == guildID {
			out = append(out, ch)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position == out[j].Position {
			return out[i].ID < out[j].ID
		}
		return out[i].Position < out[j].Position
	})
	return out, nil
}

func (m *Memory) Reorder(_ context.Context, guildID int64, ids []int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for pos, id := range ids {
		ch, ok := m.channels[id]
		if !ok || ch.GuildID != guildID {
			return apperr.Invalid("channel does not belong to guild")
		}
		ch.Position = pos
		m.channels[id] = ch
	}
	return nil
}

func (m *Memory) SaveInvite(_ context.Context, inv domain.Invite) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invites[inv.Code] = inv
	return nil
}

func (m *Memory) ConsumeInvite(_ context.Context, code string, userID int64, now time.Time) (domain.Guild, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invites[code]
	if !ok {
		return domain.Guild{}, apperr.NotFound("invite not found")
	}
	if inv.ExpiresAt != nil && now.After(*inv.ExpiresAt) {
		return domain.Guild{}, apperr.Invalid("invite expired")
	}
	if inv.MaxUses > 0 && inv.Uses >= inv.MaxUses {
		return domain.Guild{}, apperr.Invalid("invite exhausted")
	}
	g, ok := m.guilds[inv.GuildID]
	if !ok {
		return domain.Guild{}, apperr.NotFound("guild not found")
	}
	if _, exists := m.members[[2]int64{inv.GuildID, userID}]; !exists {
		m.members[[2]int64{inv.GuildID, userID}] = domain.RoleMember
		inv.Uses++
		m.invites[code] = inv
	}
	g.MyRole = m.members[[2]int64{inv.GuildID, userID}]
	return g, nil
}

func (m *Memory) Invites(_ context.Context, guildID int64) ([]domain.Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Invite
	for _, inv := range m.invites {
		if inv.GuildID == guildID {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (m *Memory) GetInvite(_ context.Context, code string) (domain.Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invites[code]
	if !ok {
		return domain.Invite{}, apperr.NotFound("invite not found")
	}
	return inv, nil
}

func (m *Memory) DeleteInvite(_ context.Context, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.invites[code]; !ok {
		return apperr.NotFound("invite not found")
	}
	delete(m.invites, code)
	return nil
}

func (m *Memory) RemoveMember(_ context.Context, guildID, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.members, [2]int64{guildID, userID})
	return nil
}

func (m *Memory) SetRole(_ context.Context, guildID, userID int64, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.members[[2]int64{guildID, userID}]; !ok {
		return apperr.NotFound("member not found")
	}
	m.members[[2]int64{guildID, userID}] = role
	return nil
}

func (m *Memory) Members(_ context.Context, guildID int64) ([]domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Member
	for key, role := range m.members {
		if key[0] == guildID {
			out = append(out, domain.Member{GuildID: guildID, UserID: key[1], Role: role})
		}
	}
	return out, nil
}
