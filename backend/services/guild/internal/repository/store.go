package repository

import (
	"context"
	"time"

	"rokko.dev/services/guild/internal/domain"
)

type Store interface {
	CreateGuild(ctx context.Context, g domain.Guild, channels []domain.Channel) error
	UpdateGuild(ctx context.Context, g domain.Guild) error
	DeleteGuild(ctx context.Context, guildID int64) error
	GetGuild(ctx context.Context, guildID int64) (domain.Guild, error)
	ListGuilds(ctx context.Context, userID int64) ([]domain.Guild, error)
	Role(ctx context.Context, guildID, userID int64) (string, error)
	SaveChannel(ctx context.Context, ch domain.Channel) error
	DeleteChannel(ctx context.Context, channelID int64) error
	Channel(ctx context.Context, channelID int64) (domain.Channel, error)
	Channels(ctx context.Context, guildID int64) ([]domain.Channel, error)
	Reorder(ctx context.Context, guildID int64, ids []int64) error
	SaveInvite(ctx context.Context, inv domain.Invite) error
	ConsumeInvite(ctx context.Context, code string, userID int64, now time.Time) (domain.Guild, error)
	Invites(ctx context.Context, guildID int64) ([]domain.Invite, error)
	GetInvite(ctx context.Context, code string) (domain.Invite, error)
	DeleteInvite(ctx context.Context, code string) error
	RemoveMember(ctx context.Context, guildID, userID int64) error
	SetRole(ctx context.Context, guildID, userID int64, role string) error
	Members(ctx context.Context, guildID int64) ([]domain.Member, error)
}
