package repository

import (
	"context"

	"rokko.dev/services/user/internal/domain"
)

type Tx interface {
	GetRel(ctx context.Context, userID, targetID int64) (string, error)
	PutRel(ctx context.Context, userID, targetID int64, relType string) error
	DelRel(ctx context.Context, userID, targetID int64) error
	AddEvent(ctx context.Context, event domain.Event) error
}

type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error
	InsertProfile(ctx context.Context, p domain.Profile) error
	UpdateProfile(ctx context.Context, p domain.Profile) (domain.Profile, error)
	GetProfile(ctx context.Context, id int64) (domain.Profile, error)
	Search(ctx context.Context, query string, limit int) ([]domain.Profile, error)
	ListByRel(ctx context.Context, userID int64, relType string) ([]domain.Profile, error)
	FriendIDs(ctx context.Context, userID int64) ([]int64, error)
	IsBlocked(ctx context.Context, a, b int64) (bool, error)
}
