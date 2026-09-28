package repository

import (
	"context"

	"rokko.dev/services/auth/internal/domain"
)

type Store interface {
	CreateAccount(ctx context.Context, account domain.Account, event domain.Event) error
	FindByEmail(ctx context.Context, email string) (domain.Account, error)
}
