package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRefreshReuseRevokesFamily(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisRefresh(rdb)
	ctx := context.Background()
	require.NoError(t, store.SaveFamily(ctx, "h1", 7, "ada", "fam", time.Hour))
	_, _, _, reused, err := store.Rotate(ctx, "h1", "h2", time.Hour)
	require.NoError(t, err)
	require.False(t, reused)
	_, _, _, reused, err = store.Rotate(ctx, "h1", "h3", time.Hour)
	require.Error(t, err)
	require.True(t, reused)
	_, err = rdb.Get(ctx, "refresh:h2").Result()
	require.Error(t, err)
}

func TestRevokeUser(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisRefresh(rdb)
	ctx := context.Background()
	require.NoError(t, store.SaveFamily(ctx, "h1", 7, "ada", "fam", time.Hour))
	require.NoError(t, store.RevokeUser(ctx, 7))
	_, _, err := store.Get(ctx, "h1")
	require.Error(t, err)
	_ = srv
}
