package state

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionsAndPresence(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	st := New(rdb)
	ctx := context.Background()
	require.NoError(t, st.Subscribe(ctx, "1", []string{"g"}, []string{"d"}))
	require.NoError(t, st.Subscribe(ctx, "2", []string{"g"}, nil))
	users, err := st.GuildUsers(ctx, "g")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"1", "2"}, users)
	require.NoError(t, st.SetOnline(ctx, "1", "c1", time.Minute))
	require.NoError(t, st.SetOnline(ctx, "1", "c2", time.Minute))
	online, err := st.Online(ctx, []string{"1", "2"})
	require.NoError(t, err)
	require.True(t, online["1"])
	require.False(t, online["2"])
	require.NoError(t, st.SetOffline(ctx, "1", "c1"))
	online, err = st.Online(ctx, []string{"1"})
	require.NoError(t, err)
	require.True(t, online["1"])
	require.NoError(t, st.SetOffline(ctx, "1", "c2"))
	online, err = st.Online(ctx, []string{"1"})
	require.NoError(t, err)
	require.False(t, online["1"])
	require.NoError(t, st.Unsubscribe(ctx, "1", []string{"g"}, []string{"d"}))
	users, err = st.GuildUsers(ctx, "g")
	require.NoError(t, err)
	require.Equal(t, []string{"2"}, users)
}

func TestSubscriptionExpiry(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	st := New(rdb)
	now := time.Now()
	st.now = func() time.Time { return now }
	ctx := context.Background()
	require.NoError(t, st.Touch(ctx, "1", "", []string{"g"}, nil, time.Second))
	now = now.Add(2 * time.Second)
	users, err := st.GuildUsers(ctx, "g")
	require.NoError(t, err)
	require.Empty(t, users)
	require.NoError(t, st.Sweep(ctx, []string{"subs:guild:g"}))
	n, err := rdb.ZCard(ctx, "subs:guild:g").Result()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestEventOnce(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	st := New(rdb)
	ctx := context.Background()
	ok, err := st.Once(ctx, "e1", time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = st.Once(ctx, "e1", time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	_ = srv
}
