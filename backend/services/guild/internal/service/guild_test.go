package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/id"
	"rokko.dev/services/guild/internal/domain"
	"rokko.dev/services/guild/internal/repository"
)

func TestGuildLifecycle(t *testing.T) {
	store := repository.NewMemory()
	svc := New(store, id.New(5))
	ctx := context.Background()

	g, err := svc.CreateGuild(ctx, 1, CreateGuildInput{Name: "Rokko"})
	require.NoError(t, err)
	channels, err := svc.ListChannels(ctx, 1, g.ID)
	require.NoError(t, err)
	require.Len(t, channels, 2)

	_, err = svc.CreateChannel(ctx, 1, g.ID, ChannelInput{Name: "off-topic", Type: "text"})
	require.NoError(t, err)

	inv, err := svc.CreateInvite(ctx, 1, g.ID, InviteInput{MaxUses: 1, ExpiresIn: 3600})
	require.NoError(t, err)
	joined, err := svc.Join(ctx, 2, inv.Code)
	require.NoError(t, err)
	require.Equal(t, domain.RoleMember, joined.MyRole)

	_, err = svc.Join(ctx, 3, inv.Code)
	require.Error(t, err)

	member, err := svc.SetRole(ctx, 1, g.ID, 2, domain.RoleAdmin)
	require.NoError(t, err)
	require.Equal(t, domain.RoleAdmin, member.Role)

	_, err = svc.CreateChannel(ctx, 2, g.ID, ChannelInput{Name: "admin-chat", Type: "text"})
	require.NoError(t, err)
	err = svc.Kick(ctx, 2, g.ID, 1)
	var app *apperr.Error
	require.ErrorAs(t, err, &app)
	require.Equal(t, apperr.CodeForbidden, app.Code)

	updated, err := svc.UpdateGuild(ctx, 2, g.ID, CreateGuildInput{Name: "Rokko HQ"})
	require.NoError(t, err)
	require.Equal(t, "Rokko HQ", updated.Name)

	err = svc.Leave(ctx, 1, g.ID)
	require.Error(t, err)
	require.NoError(t, svc.Leave(ctx, 2, g.ID))
	require.NoError(t, svc.DeleteGuild(ctx, 1, g.ID))
	_, err = svc.GetGuild(ctx, 1, g.ID)
	require.Error(t, err)
}

func TestExpiredInvite(t *testing.T) {
	store := repository.NewMemory()
	svc := New(store, id.New(6))
	svc.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	ctx := context.Background()
	g, err := svc.CreateGuild(ctx, 1, CreateGuildInput{Name: "Temp"})
	require.NoError(t, err)
	inv, err := svc.CreateInvite(ctx, 1, g.ID, InviteInput{ExpiresIn: 10})
	require.NoError(t, err)
	svc.now = func() time.Time { return time.Unix(1_700_000_000, 0).Add(time.Minute) }
	_, err = svc.Join(ctx, 9, inv.Code)
	require.Error(t, err)
}

func TestCheckChannelAccess(t *testing.T) {
	store := repository.NewMemory()
	svc := New(store, id.New(7))
	ctx := context.Background()
	g, err := svc.CreateGuild(ctx, 1, CreateGuildInput{Name: "Voice"})
	require.NoError(t, err)
	channels, err := svc.ListChannels(ctx, 1, g.ID)
	require.NoError(t, err)
	access, err := svc.CheckChannelAccess(ctx, 1, channels[0].ID)
	require.NoError(t, err)
	require.True(t, access.Allowed)
	require.True(t, access.CanModerate)
	outsider, err := svc.CheckChannelAccess(ctx, 99, channels[0].ID)
	require.NoError(t, err)
	require.False(t, outsider.Allowed)
	require.Zero(t, outsider.GuildID)
	require.Empty(t, outsider.ChannelType)
	require.Empty(t, outsider.Role)
}

type roleErrStore struct {
	repositoryStore
	err error
}

func (s roleErrStore) Role(context.Context, int64, int64) (string, error) { return "", s.err }

func TestCheckChannelAccessInfraError(t *testing.T) {
	store := repository.NewMemory()
	ctx := context.Background()
	svc := New(store, id.New(8))
	g, err := svc.CreateGuild(ctx, 1, CreateGuildInput{Name: "Voice"})
	require.NoError(t, err)
	channels, err := svc.ListChannels(ctx, 1, g.ID)
	require.NoError(t, err)

	boom := errors.New("db down")
	svc.store = roleErrStore{repositoryStore: store, err: boom}
	access, err := svc.CheckChannelAccess(ctx, 1, channels[0].ID)
	require.ErrorIs(t, err, boom)
	require.False(t, access.Allowed)
}
