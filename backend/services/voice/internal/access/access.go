package access

import (
	"context"

	guildv1 "rokko.dev/gen/guild/v1"
	messagev1 "rokko.dev/gen/message/v1"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/grpcx"
)

type Checker struct {
	guilds   guildv1.GuildServiceClient
	messages messagev1.MessageServiceClient
}

func New(guilds guildv1.GuildServiceClient, messages messagev1.MessageServiceClient) Checker {
	return Checker{guilds: guilds, messages: messages}
}

func (c Checker) Join(ctx context.Context, token, roomID, kind string) ([]string, string, error) {
	switch kind {
	case "guild":
		resp, err := c.guilds.CheckChannelAccess(grpcx.WithToken(ctx, token), &guildv1.CheckChannelAccessRequest{ChannelId: roomID})
		if err != nil {
			return nil, "", err
		}
		if !resp.GetAllowed() || resp.GetChannelType() != "voice" {
			return nil, "", apperr.Forbidden("no access to voice channel")
		}
		return nil, resp.GetGuildId(), nil
	case "dm":
		resp, err := c.messages.CheckDMAccess(grpcx.WithToken(ctx, token), &messagev1.CheckDMAccessRequest{ChannelId: roomID})
		if err != nil {
			return nil, "", err
		}
		if !resp.GetAllowed() {
			return nil, "", apperr.Forbidden("no access to dm")
		}
		return resp.GetMemberIds(), "", nil
	default:
		return nil, "", apperr.Invalid("room kind must be guild or dm")
	}
}
