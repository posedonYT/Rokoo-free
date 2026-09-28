package events

import (
	"context"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/services/user/internal/repository"
	"rokko.dev/services/user/internal/service"
)

func TestRegisteredEventCreatesProfile(t *testing.T) {
	svc := service.New(repository.NewMemory(), id.New(2))
	env, err := eventx.New("1", "user.registered", map[string]string{"user_id": "42", "username": "neo"})
	require.NoError(t, err)
	raw, err := env.Bytes()
	require.NoError(t, err)
	require.NoError(t, Handle(svc)(context.Background(), kafka.Message{Value: raw}))
	profile, err := svc.Get(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "neo", profile.Username)
	require.NoError(t, Handle(svc)(context.Background(), kafka.Message{Value: raw}))
	again, err := svc.Get(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, profile.Discriminator, again.Discriminator)
}
