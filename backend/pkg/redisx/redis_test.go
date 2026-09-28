package redisx

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func client(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	srv := miniredis.RunT(t)
	return srv, New(Config{Addr: srv.Addr()})
}

func TestAllowWindow(t *testing.T) {
	srv, rdb := client(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		ok, err := Allow(ctx, rdb, "limit:login", 3, time.Minute)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := Allow(ctx, rdb, "limit:login", 3, time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	srv.FastForward(2 * time.Minute)
	ok, err = Allow(ctx, rdb, "limit:login", 3, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestAllowConcurrent(t *testing.T) {
	_, rdb := client(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var allowed atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := Allow(ctx, rdb, "burst", 10, time.Minute)
			require.NoError(t, err)
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int64(10), allowed.Load())
}

func TestAllowSliding(t *testing.T) {
	_, rdb := client(t)
	ctx := context.Background()
	now := time.UnixMilli(1_000_000)
	ok, err := AllowSliding(ctx, rdb, "slide", 1, time.Second, now, "a")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = AllowSliding(ctx, rdb, "slide", 1, time.Second, now.Add(10*time.Millisecond), "b")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = AllowSliding(ctx, rdb, "slide", 1, time.Second, now.Add(2*time.Second), "c")
	require.NoError(t, err)
	require.True(t, ok)
}
