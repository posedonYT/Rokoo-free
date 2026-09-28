package clients

import (
	"context"

	guildv1 "rokko.dev/gen/guild/v1"
	userv1 "rokko.dev/gen/user/v1"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/id"
)

type Guild struct{ api guildv1.GuildServiceClient }

func NewGuild(api guildv1.GuildServiceClient) Guild { return Guild{api: api} }

func (g Guild) Check(ctx context.Context, token string, channelID int64) (int64, string, bool, bool, error) {
	resp, err := g.api.CheckChannelAccess(grpcx.WithToken(ctx, token), &guildv1.CheckChannelAccessRequest{ChannelId: id.Format(channelID)})
	if err != nil {
		return 0, "", false, false, err
	}
	guildID, err := id.ParseID(resp.GetGuildId())
	if err != nil && resp.GetAllowed() {
		return 0, "", false, false, err
	}
	return guildID, resp.GetChannelType(), resp.GetCanModerate(), resp.GetAllowed(), nil
}

type Blocks struct{ api userv1.UserServiceClient }

func NewBlocks(api userv1.UserServiceClient) Blocks { return Blocks{api: api} }

func (b Blocks) IsBlocked(ctx context.Context, token string, a, c int64) (bool, error) {
	resp, err := b.api.IsBlocked(grpcx.WithToken(ctx, token), &userv1.IsBlockedRequest{UserA: id.Format(a), UserB: id.Format(c)})
	if err != nil {
		return false, err
	}
	return resp.GetBlocked(), nil
}
