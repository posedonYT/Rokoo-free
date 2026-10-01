package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/id"
	"rokko.dev/services/auth/internal/domain"
)

type memStore struct {
	byEmail map[string]domain.Account
	events  int
}

func (m *memStore) CreateAccount(_ context.Context, account domain.Account, _ domain.Event) error {
	if _, ok := m.byEmail[account.Email]; ok {
		return apperr.Conflict("email already registered")
	}
	m.byEmail[account.Email] = account
	m.events++
	return nil
}

func (m *memStore) FindByEmail(_ context.Context, email string) (domain.Account, error) {
	a, ok := m.byEmail[email]
	if !ok {
		return domain.Account{}, apperr.Unauthorized("invalid email or password")
	}
	return a, nil
}

type memRefresh struct {
	items map[string]struct {
		id   int64
		name string
	}
}

func (m *memRefresh) Save(_ context.Context, hash string, userID int64, username string, _ time.Duration) error {
	m.items[hash] = struct {
		id   int64
		name string
	}{userID, username}
	return nil
}

func (m *memRefresh) Get(_ context.Context, hash string) (int64, string, error) {
	v, ok := m.items[hash]
	if !ok {
		return 0, "", apperr.Unauthorized("invalid refresh token")
	}
	return v.id, v.name, nil
}

func (m *memRefresh) Delete(_ context.Context, hash string) error {
	delete(m.items, hash)
	return nil
}

type memLimit struct {
	max    int
	counts map[string]int
}

func (m *memLimit) Allow(_ context.Context, key string) (bool, error) {
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[key]++
	return m.counts[key] <= m.max, nil
}

func (m *memLimit) Reset(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(m.counts, k)
	}
	return nil
}

type ensured struct{ calls int }

func (e *ensured) Ensure(context.Context, string, int64, string) error {
	e.calls++
	return nil
}

func newTokens(t *testing.T) *authn.Manager {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	dir := t.TempDir()
	privPEM, err := jwt.ParseEdPrivateKeyFromPEM(mustPKCS8(t, priv))
	require.NoError(t, err)
	_ = privPEM
	require.NoError(t, os.WriteFile(filepath.Join(dir, "priv.pem"), mustPKCS8(t, priv), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pub.pem"), mustPKIX(t, pub), 0o644))
	m, err := authn.Load(filepath.Join(dir, "priv.pem"), filepath.Join(dir, "pub.pem"), "rokko", time.Minute)
	require.NoError(t, err)
	return m
}

func TestRegisterLoginRefreshLogout(t *testing.T) {
	store := &memStore{byEmail: map[string]domain.Account{}}
	refresh := &memRefresh{items: map[string]struct {
		id   int64
		name string
	}{}}
	profiles := &ensured{}
	svc := New(store, refresh, newTokens(t), id.New(1), &memLimit{max: 5}, profiles, time.Hour)

	tokens, err := svc.Register(context.Background(), RegisterInput{
		Email: "Ada@Example.com", Username: "Ada_1", Password: "supersecret",
	})
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessToken)
	require.Equal(t, 1, store.events)
	require.Equal(t, 1, profiles.calls)
	require.Equal(t, "ada_1", tokens.Username)

	_, err = svc.Register(context.Background(), RegisterInput{
		Email: "ada@example.com", Username: "ada_1", Password: "supersecret",
	})
	require.Error(t, err)

	_, err = svc.Login(context.Background(), LoginInput{Email: "ada@example.com", Password: "nope-nope"})
	require.Error(t, err)

	logged, err := svc.Login(context.Background(), LoginInput{Email: "ada@example.com", Password: "supersecret"})
	require.NoError(t, err)

	rotated, err := svc.Refresh(context.Background(), logged.RefreshToken)
	require.NoError(t, err)
	_, err = svc.Refresh(context.Background(), logged.RefreshToken)
	require.Error(t, err)

	require.NoError(t, svc.Logout(context.Background(), rotated.RefreshToken))
	_, err = svc.Refresh(context.Background(), rotated.RefreshToken)
	require.Error(t, err)
}

func TestLoginRateLimitCountsOnlyFailures(t *testing.T) {
	store := &memStore{byEmail: map[string]domain.Account{}}
	refresh := &memRefresh{items: map[string]struct {
		id   int64
		name string
	}{}}
	lim := &memLimit{max: 2}
	svc := New(store, refresh, newTokens(t), id.New(1), lim, nil, time.Hour)
	ctx := context.Background()
	_, err := svc.Register(ctx, RegisterInput{Email: "a@b.co", Username: "abc", Password: "supersecret", IP: "10.0.0.1"})
	require.NoError(t, err)

	// successful logins never accumulate
	for i := 0; i < 5; i++ {
		_, err = svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "supersecret", IP: "10.0.0.1"})
		require.NoError(t, err)
	}
	require.Empty(t, lim.counts["login:ip:10.0.0.1"])
	require.Empty(t, lim.counts["login:a@b.co:10.0.0.1"])

	// failed attempts accumulate, then block even the correct password
	for i := 0; i < 2; i++ {
		_, err = svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "wrong-pass", IP: "10.0.0.1"})
		require.Error(t, err)
	}
	require.Equal(t, 2, lim.counts["login:ip:10.0.0.1"])
	require.Equal(t, 2, lim.counts["login:a@b.co:10.0.0.1"])
	_, err = svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "supersecret", IP: "10.0.0.1"})
	var app *apperr.Error
	require.ErrorAs(t, err, &app)
	require.Equal(t, apperr.CodeRateLimit, app.Code)

	// another IP is not affected by the per-email:ip key
	_, err = svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "supersecret", IP: "10.0.0.2"})
	require.NoError(t, err)
}

func TestLoginSuccessResetsFailures(t *testing.T) {
	store := &memStore{byEmail: map[string]domain.Account{}}
	refresh := &memRefresh{items: map[string]struct {
		id   int64
		name string
	}{}}
	lim := &memLimit{max: 3}
	svc := New(store, refresh, newTokens(t), id.New(1), lim, nil, time.Hour)
	ctx := context.Background()
	_, err := svc.Register(ctx, RegisterInput{Email: "a@b.co", Username: "abc", Password: "supersecret", IP: "10.0.0.1"})
	require.NoError(t, err)
	_, err = svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "wrong-pass", IP: "10.0.0.1"})
	require.Error(t, err)
	require.Equal(t, 1, lim.counts["login:ip:10.0.0.1"])
	_, err = svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "supersecret", IP: "10.0.0.1"})
	require.NoError(t, err)
	require.Empty(t, lim.counts["login:ip:10.0.0.1"])
	require.Empty(t, lim.counts["login:a@b.co:10.0.0.1"])
}

func TestRegisterRateLimitByIP(t *testing.T) {
	store := &memStore{byEmail: map[string]domain.Account{}}
	refresh := &memRefresh{items: map[string]struct {
		id   int64
		name string
	}{}}
	lim := &memLimit{max: 2}
	svc := New(store, refresh, newTokens(t), id.New(1), lim, nil, time.Hour)
	ctx := context.Background()
	for i, email := range []string{"a@b.co", "b@b.co"} {
		_, err := svc.Register(ctx, RegisterInput{Email: email, Username: "user" + string(rune('a'+i)), Password: "supersecret", IP: "10.0.0.1"})
		require.NoError(t, err)
	}
	_, err := svc.Register(ctx, RegisterInput{Email: "c@b.co", Username: "userc", Password: "supersecret", IP: "10.0.0.1"})
	var app *apperr.Error
	require.ErrorAs(t, err, &app)
	require.Equal(t, apperr.CodeRateLimit, app.Code)
	require.Equal(t, 3, lim.counts["register:ip:10.0.0.1"])

	_, err = svc.Register(ctx, RegisterInput{Email: "c@b.co", Username: "userc", Password: "supersecret", IP: "10.0.0.2"})
	require.NoError(t, err)
}

func TestRegisterValidation(t *testing.T) {
	svc := New(&memStore{byEmail: map[string]domain.Account{}}, &memRefresh{items: map[string]struct {
		id   int64
		name string
	}{}}, newTokens(t), id.New(1), nil, nil, time.Hour)
	_, err := svc.Register(context.Background(), RegisterInput{Email: "bad", Username: "a", Password: "short"})
	require.Error(t, err)
}
