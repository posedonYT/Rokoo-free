package registry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestClaimRefreshRelease(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	reg := New(rdb, time.Second)
	ctx := context.Background()
	owner, err := reg.Claim(ctx, "dm:1", "node-a")
	require.NoError(t, err)
	require.Equal(t, "node-a", owner)
	owner, err = reg.Claim(ctx, "dm:1", "node-b")
	require.NoError(t, err)
	require.Equal(t, "node-a", owner)
	ok, err := reg.Refresh(ctx, "dm:1", "node-b")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = reg.Refresh(ctx, "dm:1", "node-a")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, reg.SetParticipants(ctx, "dm:1", []string{"u1", "u2"}))
	parts, err := reg.Participants(ctx, "dm:1")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"u1", "u2"}, parts)
	require.NoError(t, reg.UpsertNode(ctx, Node{ID: "node-a", URL: "ws://a", Load: 1}))
	nodes, err := reg.Nodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.NoError(t, reg.Release(ctx, "dm:1", "node-a"))
	owner, err = reg.Owner(ctx, "dm:1")
	require.NoError(t, err)
	require.Empty(t, owner)
}

func TestClaimExpires(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	reg := New(rdb, time.Second)
	ctx := context.Background()
	_, err := reg.Claim(ctx, "dm:2", "node-a")
	require.NoError(t, err)
	srv.FastForward(2 * time.Second)
	owner, err := reg.Claim(ctx, "dm:2", "node-b")
	require.NoError(t, err)
	require.Equal(t, "node-b", owner)
}

func TestConcurrentClaim(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	reg := New(rdb, time.Minute)
	ctx := context.Background()
	var wg sync.WaitGroup
	owners := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			owner, err := reg.Claim(ctx, "room", "n")
			if err == nil {
				owners <- owner
			}
		}(i)
	}
	wg.Wait()
	close(owners)
	for owner := range owners {
		require.Equal(t, "n", owner)
	}
}
