//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"rokko.dev/pkg/migratex"
	"rokko.dev/pkg/postgres"
	"rokko.dev/services/guild/internal/domain"
	"rokko.dev/services/guild/migrations"
)

func TestGuildOutboxRollsBackWithMutation(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("guilds"),
		tcpostgres.WithUsername("rokko"),
		tcpostgres.WithPassword("rokko"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, pg)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, migratex.Up(dsn, migrations.FS))
	require.NoError(t, migratex.Down(dsn, migrations.FS))
	require.NoError(t, migratex.Up(dsn, migrations.FS))
	pool, err := postgres.NewPool(ctx, postgres.Config{DSN: dsn, ApplicationName: "guild-test", StatementTimeout: "1s"})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store := NewPostgres(pool)
	require.NoError(t, store.CreateGuild(ctx, domain.Guild{ID: 1, Name: "g", OwnerID: 2}, []domain.Channel{{ID: 3, GuildID: 1, Name: "voice", Type: domain.ChannelVoice, Position: 0}}))
	err = store.SetRole(ctx, 1, 2, "nope")
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code)
	var pending int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&pending))
	require.Equal(t, 1, pending)
	_, err = pool.Exec(ctx, `SELECT pg_sleep(2)`)
	require.Error(t, err)
}
