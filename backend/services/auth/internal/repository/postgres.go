package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/outbox"
	"rokko.dev/services/auth/internal/domain"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) CreateAccount(ctx context.Context, account domain.Account, event domain.Event) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO accounts (id, email, username, password_hash)
		VALUES ($1, $2, $3, $4)
	`, account.ID, account.Email, account.Username, account.PasswordHash)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return apperr.Conflict("email already registered")
		}
		return err
	}
	if err := outbox.Insert(ctx, tx, event.ID, event.Topic, event.Key, event.Payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) FindByEmail(ctx context.Context, email string) (domain.Account, error) {
	var a domain.Account
	err := p.pool.QueryRow(ctx, `
		SELECT id, email, username, password_hash FROM accounts WHERE email = $1
	`, email).Scan(&a.ID, &a.Email, &a.Username, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, apperr.Unauthorized("invalid email or password")
	}
	return a, err
}
