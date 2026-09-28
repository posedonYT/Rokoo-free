package repository

import (
	"context"

	"rokko.dev/services/message/internal/domain"
)

type MessageRepository interface {
	InsertMessage(ctx context.Context, msg domain.Message, event domain.Event) error
	UpdateMessage(ctx context.Context, msg domain.Message, event domain.Event) error
	SoftDelete(ctx context.Context, messageID int64, event domain.Event) error
	GetMessage(ctx context.Context, messageID int64) (domain.Message, error)
	ListMessages(ctx context.Context, channelID int64, before int64, limit int) ([]domain.Message, error)
}

type DMRepository interface {
	FindPair(ctx context.Context, a, b int64) (domain.DMChannel, bool, error)
	CreateDM(ctx context.Context, ch domain.DMChannel) error
	DM(ctx context.Context, channelID int64) (domain.DMChannel, error)
	ListDMs(ctx context.Context, userID int64) ([]domain.DMChannel, error)
	AddMember(ctx context.Context, channelID, userID int64) error
	RemoveMember(ctx context.Context, channelID, userID int64) error
}
