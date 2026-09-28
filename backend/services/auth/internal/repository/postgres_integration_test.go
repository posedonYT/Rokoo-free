//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"rokko.dev/pkg/migratex"
	"rokko.dev/services/auth/internal/domain"
	"rokko.dev/services/auth/migrations"
)

func TestPostgresCreateAndFind(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("auth"),
		postgres.WithUsername("rokko"),
		postgres.WithPassword("rokko"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, pg)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, migratex.Up(dsn, migrations.FS))

	pool := mustPool(t, ctx, dsn)
	store := NewPostgres(pool)
	err = store.CreateAccount(ctx, domain.Account{ID: 10, Email: "a@b.co", Username: "ada", PasswordHash: "hash"}, domain.Event{
		ID: 11, Topic: "user.events", Key: "10", Payload: []byte(`{"type":"user.registered"}`),
	})
	require.NoError(t, err)
	err = store.CreateAccount(ctx, domain.Account{ID: 12, Email: "a@b.co", Username: "ada", PasswordHash: "hash"}, domain.Event{ID: 13, Topic: "user.events", Key: "12", Payload: []byte(`{}`)})
	require.Error(t, err)
	got, err := store.FindByEmail(ctx, "a@b.co")
	require.NoError(t, err)
	require.Equal(t, int64(10), got.ID)

	var pending int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&pending))
	require.Equal(t, 1, pending)
}
