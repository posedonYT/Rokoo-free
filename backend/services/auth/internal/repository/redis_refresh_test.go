package repository

import (
	"context"
	"fmt"
	"sync"
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
	uid, name, fam, reused, err := store.Rotate(ctx, "h1", "h2", time.Hour)
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, int64(7), uid)
	require.Equal(t, "ada", name)
	require.Equal(t, "fam", fam)

	srv.FastForward(RefreshGrace + time.Second)
	_, _, _, reused, err = store.Rotate(ctx, "h1", "h3", time.Hour)
	require.Error(t, err)
	require.True(t, reused)
	_, err = rdb.Get(ctx, "refresh:h2").Result()
	require.Error(t, err)
	_, err = rdb.Get(ctx, "refresh:h3").Result()
	require.Error(t, err)
}

func TestRefreshParallelRotateWithinGrace(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisRefresh(rdb)
	ctx := context.Background()
	require.NoError(t, store.SaveFamily(ctx, "h1", 7, "ada", "fam", time.Hour))

	_, _, _, reused, err := store.Rotate(ctx, "h1", "h2", time.Hour)
	require.NoError(t, err)
	require.False(t, reused)

	uid, name, fam, reused, err := store.Rotate(ctx, "h1", "h3", time.Hour)
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, int64(7), uid)
	require.Equal(t, "ada", name)
	require.Equal(t, "fam", fam)

	for _, h := range []string{"h2", "h3"} {
		u, n, err := store.Get(ctx, h)
		require.NoError(t, err)
		require.Equal(t, int64(7), u)
		require.Equal(t, "ada", n)
	}
	grace, err := rdb.Get(ctx, "refresh_grace:h1").Result()
	require.NoError(t, err)
	require.Equal(t, "h2|7|ada|fam", grace)
	require.LessOrEqual(t, srv.TTL("refresh_grace:h1"), RefreshGrace)
	_, err = rdb.Get(ctx, "refresh:h1").Result()
	require.ErrorIs(t, err, redis.Nil)
}

func TestRefreshConcurrentRotateDoesNotRevoke(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisRefresh(rdb)
	ctx := context.Background()
	require.NoError(t, store.SaveFamily(ctx, "h1", 7, "ada", "fam", time.Hour))

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	reusedFlags := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _, reusedFlags[i], errs[i] = store.Rotate(ctx, "h1", fmt.Sprintf("new%d", i), time.Hour)
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.False(t, reusedFlags[i])
		_, _, err := store.Get(ctx, fmt.Sprintf("new%d", i))
		require.NoError(t, err)
	}
}

func TestRefreshRotateUnknownToken(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisRefresh(rdb)
	_, _, _, reused, err := store.Rotate(context.Background(), "nope", "h2", time.Hour)
	require.Error(t, err)
	require.False(t, reused)
}

func TestRedisLimiterAllowAndReset(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	l := NewRedisLimiter(rdb, 2, time.Minute)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		ok, err := l.Allow(ctx, "login:ip:1.1.1.1")
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := l.Allow(ctx, "login:ip:1.1.1.1")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, l.Reset(ctx, "login:ip:1.1.1.1"))
	ok, err = l.Allow(ctx, "login:ip:1.1.1.1")
	require.NoError(t, err)
	require.True(t, ok)
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
