//go:build integration

package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgxPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, dsn)
}
