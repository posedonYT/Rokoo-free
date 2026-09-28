package events

import (
	"context"
	"encoding/json"

	"github.com/segmentio/kafka-go"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/services/user/internal/service"
)

type registered struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

func Handle(svc *service.Service) func(context.Context, kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var env eventx.Envelope
		if err := json.Unmarshal(msg.Value, &env); err != nil {
			return nil
		}
		if env.Type != "user.registered" {
			return nil
		}
		var payload registered
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return nil
		}
		userID, err := id.ParseID(payload.UserID)
		if err != nil {
			return nil
		}
		_, err = svc.EnsureProfile(ctx, userID, payload.Username)
		return err
	}
}
