package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/eventx"
	"rokko.dev/services/realtime/internal/hub"
	"rokko.dev/services/realtime/internal/state"
)

func newTestServer(t *testing.T) (*Server, *state.Redis) {
	t.Helper()
	mr := miniredis.RunT(t)
	st := state.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	return New(nil, hub.New(), st, nil, nil, nil), st
}

func TestDeliverDoesNotMarkOnPublishFailure(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()
	fail := true
	srv.publish = func(ctx context.Context, userID string, payload []byte) error {
		if fail {
			return errors.New("redis down")
		}
		return nil
	}
	env, err := eventx.New("evt-1", "message.created", map[string]string{"x": "y"})
	require.NoError(t, err)
	env.RecipientIDs = []string{"1", "2"}
	require.Error(t, srv.Deliver(ctx, env))
	seen, err := st.Seen(ctx, "evt-1")
	require.NoError(t, err)
	require.False(t, seen)

	fail = false
	require.NoError(t, srv.Deliver(ctx, env))
	seen, err = st.Seen(ctx, "evt-1")
	require.NoError(t, err)
	require.True(t, seen)

	calls := 0
	srv.publish = func(ctx context.Context, userID string, payload []byte) error { calls++; return nil }
	require.NoError(t, srv.Deliver(ctx, env))
	require.Zero(t, calls, "duplicate must be skipped")
}

func TestTokenFromRequestPrefersProtocol(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/ws?token=query", nil)
	r.Header.Set("Authorization", "Bearer hdr")
	require.Equal(t, "query", tokenFromRequest(r))
	r.Header.Set("Sec-WebSocket-Protocol", "rokko.v1, proto.jwt")
	require.Equal(t, "proto.jwt", tokenFromRequest(r))
	r = httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.Header.Set("Authorization", "Bearer hdr")
	require.Equal(t, "hdr", tokenFromRequest(r))
}

func TestTypingRequiresSubscription(t *testing.T) {
	srv, _ := newTestServer(t)
	st := &connState{guilds: map[string]struct{}{}, dms: map[string]struct{}{"d1": {}}, channels: map[string]string{}}
	ctx := context.Background()
	require.True(t, srv.typingAllowed(ctx, st, "d1", ""))
	require.False(t, srv.typingAllowed(ctx, st, "d2", ""))
	require.False(t, srv.typingAllowed(ctx, st, "c1", "g1"))
}

func TestCapUnique(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, capUnique([]string{"a", "", "a", "b", "c"}, 2))
}
