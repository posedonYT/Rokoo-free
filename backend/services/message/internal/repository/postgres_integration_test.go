//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"rokko.dev/pkg/migratex"
	"rokko.dev/services/message/internal/domain"
	"rokko.dev/services/message/migrations"
)

func TestMessageCursor(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("messages"),
		postgres.WithUsername("rokko"),
		postgres.WithPassword("rokko"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, pg)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, migratex.Up(dsn, migrations.FS))
	pool, err := pgxPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store := NewPostgres(pool)
	require.NoError(t, store.CreateDM(ctx, domain.DMChannel{ID: 1, Kind: domain.KindDM, Members: []int64{2, 3}}))
	for _, id := range []int64{10, 11, 12} {
		require.NoError(t, store.InsertMessage(ctx, domain.Message{
			ID: id, ChannelID: 1, ChannelKind: domain.KindDM, AuthorID: 2, Content: "m",
		}, domain.Event{}))
	}
	page, err := store.ListMessages(ctx, 1, 0, 2)
	require.NoError(t, err)
	require.Equal(t, []int64{12, 11}, []int64{page[0].ID, page[1].ID})
	older, err := store.ListMessages(ctx, 1, page[1].ID, 10)
	require.NoError(t, err)
	require.Equal(t, int64(10), older[0].ID)
}
