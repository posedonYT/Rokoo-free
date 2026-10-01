package events

import (
	"context"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/eventx"
	"rokko.dev/services/message/internal/domain"
	"rokko.dev/services/message/internal/repository"
)

func seed(t *testing.T) *repository.Memory {
	t.Helper()
	store := repository.NewMemory()
	ctx := context.Background()
	for _, m := range []domain.Message{
		{ID: 1, ChannelID: 10, ChannelKind: domain.KindGuild, Content: "a"},
		{ID: 2, ChannelID: 11, ChannelKind: domain.KindGuild, Content: "b"},
		{ID: 3, ChannelID: 12, ChannelKind: domain.KindGuild, Content: "c"},
		{ID: 4, ChannelID: 10, ChannelKind: domain.KindDM, Content: "dm"},
	} {
		require.NoError(t, store.InsertMessage(ctx, m, domain.Event{}))
	}
	return store
}

func send(t *testing.T, store repository.ChannelCleaner, typ string, payload any) error {
	t.Helper()
	env, err := eventx.New("1", typ, payload)
	require.NoError(t, err)
	raw, err := env.Bytes()
	require.NoError(t, err)
	return Handle(store)(context.Background(), kafka.Message{Value: raw})
}

func count(t *testing.T, store *repository.Memory, channelID int64) int {
	t.Helper()
	list, err := store.ListMessages(context.Background(), channelID, 0, 100)
	require.NoError(t, err)
	return len(list)
}

func TestChannelDeletedRemovesMessages(t *testing.T) {
	store := seed(t)
	require.NoError(t, send(t, store, "channel.deleted", map[string]string{"guild_id": "1", "channel_id": "10"}))
	require.Equal(t, 1, count(t, store, 10))
	require.Equal(t, 1, count(t, store, 11))
}

func TestGuildDeletedRemovesAllChannels(t *testing.T) {
	store := seed(t)
	require.NoError(t, send(t, store, "guild.deleted", map[string]any{"guild_id": "1", "channel_ids": []string{"10", "11"}}))
	require.Equal(t, 1, count(t, store, 10))
	require.Equal(t, 0, count(t, store, 11))
	require.Equal(t, 1, count(t, store, 12))
}

func TestGuildDeletedWithoutChannelsIsNoop(t *testing.T) {
	store := seed(t)
	require.NoError(t, send(t, store, "guild.deleted", map[string]string{"guild_id": "1"}))
	require.Equal(t, 3, count(t, store, 10)+count(t, store, 11))
}

func TestIgnoresOtherEvents(t *testing.T) {
	store := seed(t)
	require.NoError(t, send(t, store, "guild.updated", map[string]string{"guild_id": "1"}))
	require.Equal(t, 1, count(t, store, 12))
}
