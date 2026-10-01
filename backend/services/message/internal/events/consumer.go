package events

import (
	"context"
	"encoding/json"

	"github.com/segmentio/kafka-go"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/services/message/internal/repository"
)

type channelDeleted struct {
	ChannelID string `json:"channel_id"`
}

type guildDeleted struct {
	GuildID    string   `json:"guild_id"`
	ChannelIDs []string `json:"channel_ids"`
}

func Handle(store repository.ChannelCleaner) func(context.Context, kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var env eventx.Envelope
		if err := json.Unmarshal(msg.Value, &env); err != nil {
			return nil
		}
		var raw []string
		switch env.Type {
		case "channel.deleted":
			var p channelDeleted
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return nil
			}
			raw = []string{p.ChannelID}
		case "guild.deleted":
			var p guildDeleted
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return nil
			}
			raw = p.ChannelIDs
		default:
			return nil
		}
		ids := make([]int64, 0, len(raw))
		for _, s := range raw {
			v, err := id.ParseID(s)
			if err != nil {
				continue
			}
			ids = append(ids, v)
		}
		if len(ids) == 0 {
			return nil
		}
		_, err := store.DeleteByChannelIDs(ctx, ids)
		return err
	}
}
