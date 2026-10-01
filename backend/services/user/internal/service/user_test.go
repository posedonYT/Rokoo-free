package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/id"
	"rokko.dev/services/user/internal/domain"
	"rokko.dev/services/user/internal/repository"
)

func TestFriendsAndBlocks(t *testing.T) {
	store := repository.NewMemory()
	svc := New(store, id.New(2))
	ctx := context.Background()

	a, err := svc.EnsureProfile(ctx, 1, "ada")
	require.NoError(t, err)
	require.Equal(t, "ada", a.Username)
	require.Len(t, a.Discriminator, 4)
	again, err := svc.EnsureProfile(ctx, 1, "ada")
	require.NoError(t, err)
	require.Equal(t, a.Discriminator, again.Discriminator)

	_, err = svc.EnsureProfile(ctx, 2, "bob")
	require.NoError(t, err)

	status, err := svc.SendFriendRequest(ctx, 1, 2)
	require.NoError(t, err)
	require.Equal(t, domain.RelPendingOut, status)

	_, err = svc.SendFriendRequest(ctx, 1, 2)
	require.Error(t, err)

	require.NoError(t, svc.Accept(ctx, 2, 1))
	friends, err := svc.List(ctx, 1, domain.RelFriend)
	require.NoError(t, err)
	require.Len(t, friends, 1)

	require.NoError(t, svc.Block(ctx, 1, 2))
	blocked, err := svc.IsBlocked(ctx, 2, 1)
	require.NoError(t, err)
	require.True(t, blocked)
	_, err = svc.SendFriendRequest(ctx, 2, 1)
	var app *apperr.Error
	require.ErrorAs(t, err, &app)
	require.Equal(t, apperr.CodeForbidden, app.Code)

	require.NoError(t, svc.Unblock(ctx, 1, 2))
	status, err = svc.SendFriendRequest(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, domain.RelPendingOut, status)
	status, err = svc.SendFriendRequest(ctx, 1, 2)
	require.NoError(t, err)
	require.Equal(t, domain.RelFriend, status)
}

func TestDeclineAndRemove(t *testing.T) {
	store := repository.NewMemory()
	svc := New(store, id.New(3))
	ctx := context.Background()
	_, err := svc.EnsureProfile(ctx, 10, "cara")
	require.NoError(t, err)
	_, err = svc.EnsureProfile(ctx, 11, "dan")
	require.NoError(t, err)
	_, err = svc.SendFriendRequest(ctx, 10, 11)
	require.NoError(t, err)
	require.NoError(t, svc.Decline(ctx, 11, 10))
	incoming, err := svc.List(ctx, 11, domain.RelPendingIn)
	require.NoError(t, err)
	require.Empty(t, incoming)

	_, err = svc.SendFriendRequest(ctx, 10, 11)
	require.NoError(t, err)
	require.NoError(t, svc.Accept(ctx, 11, 10))
	require.NoError(t, svc.RemoveFriend(ctx, 10, 11))
	friends, err := svc.FriendIDs(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, friends)
}

func TestSearchAndUpdate(t *testing.T) {
	store := repository.NewMemory()
	svc := New(store, id.New(4))
	ctx := context.Background()
	p, err := svc.EnsureProfile(ctx, 5, "eve")
	require.NoError(t, err)
	updated, err := svc.UpdateProfile(ctx, 5, UpdateInput{DisplayName: "Eve", StatusText: "away"})
	require.NoError(t, err)
	require.Equal(t, "Eve", updated.DisplayName)
	found, err := svc.Search(ctx, "eve#"+p.Discriminator, 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
}

func TestSearchPrefixAndTag(t *testing.T) {
	store := repository.NewMemory()
	svc := New(store, id.New(4))
	ctx := context.Background()
	a, err := svc.EnsureProfile(ctx, 21, "alice")
	require.NoError(t, err)
	_, err = svc.EnsureProfile(ctx, 22, "alina")
	require.NoError(t, err)

	found, err := svc.Search(ctx, "ali", 10)
	require.NoError(t, err)
	require.Len(t, found, 2)

	found, err = svc.Search(ctx, "alice#"+a.Discriminator, 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, int64(21), found[0].ID)

	// exact tag with wrong discriminator must not fall back to prefix
	found, err = svc.Search(ctx, "alice#nope", 10)
	require.NoError(t, err)
	require.Empty(t, found)

	// LIKE wildcards are treated literally
	for _, q := range []string{"%", "_", "a%", "al_ce"} {
		found, err = svc.Search(ctx, q, 10)
		require.NoError(t, err)
		require.Empty(t, found, q)
	}
}
