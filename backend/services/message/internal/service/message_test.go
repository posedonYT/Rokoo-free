package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/id"
	"rokko.dev/services/message/internal/domain"
	"rokko.dev/services/message/internal/repository"
)

type guildFake struct {
	channels map[int64]struct {
		guild int64
		typ   string
		mod   bool
	}
	members map[int64]map[int64]bool
}

func (g guildFake) Check(_ context.Context, _ string, channelID int64) (int64, string, bool, bool, error) {
	ch, ok := g.channels[channelID]
	if !ok {
		return 0, "", false, false, apperr.NotFound("channel")
	}
	return ch.guild, ch.typ, ch.mod, true, nil
}

type blockFake struct{ pairs map[[2]int64]bool }

func (b blockFake) IsBlocked(_ context.Context, _ string, a, c int64) (bool, error) {
	return b.pairs[[2]int64{a, c}] || b.pairs[[2]int64{c, a}], nil
}

func TestDMAndPagination(t *testing.T) {
	mem := repository.NewMemory()
	svc := New(mem, mem, guildFake{}, blockFake{}, id.New(8))
	ctx := context.Background()
	dm, err := svc.OpenDM(ctx, 1, 2, "tok")
	require.NoError(t, err)
	again, err := svc.OpenDM(ctx, 2, 1, "tok")
	require.NoError(t, err)
	require.Equal(t, dm.ID, again.ID)

	var first int64
	for i := 0; i < 3; i++ {
		msg, err := svc.Send(ctx, 1, SendInput{ChannelID: dm.ID, ChannelKind: domain.KindDM, Content: "hi", Token: "tok"})
		require.NoError(t, err)
		if i == 0 {
			first = msg.ID
		}
	}
	page, err := svc.List(ctx, 2, dm.ID, domain.KindDM, "tok", 0, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.Less(t, page[0].ID, page[1].ID)
	older, err := svc.List(ctx, 2, dm.ID, domain.KindDM, "tok", page[0].ID, 50)
	require.NoError(t, err)
	require.Equal(t, first, older[0].ID)

	edited, err := svc.Edit(ctx, 1, first, "edited", "tok")
	require.NoError(t, err)
	require.Equal(t, "edited", edited.Content)
	err = svc.Delete(ctx, 2, first, "tok")
	require.Error(t, err)
	require.NoError(t, svc.Delete(ctx, 1, first, "tok"))
}

func TestGroupLimitAndGuildModeration(t *testing.T) {
	mem := repository.NewMemory()
	guilds := guildFake{channels: map[int64]struct {
		guild int64
		typ   string
		mod   bool
	}{42: {guild: 7, typ: "text", mod: true}, 43: {guild: 7, typ: "voice", mod: false}}}
	svc := New(mem, mem, guilds, blockFake{pairs: map[[2]int64]bool{{1, 9}: true}}, id.New(9))
	ctx := context.Background()
	_, err := svc.CreateGroup(ctx, 1, []int64{2}, "tok")
	require.Error(t, err)
	group, err := svc.CreateGroup(ctx, 1, []int64{2, 3}, "tok")
	require.NoError(t, err)
	require.Equal(t, domain.KindGroup, group.Kind)
	_, err = svc.OpenDM(ctx, 1, 9, "tok")
	var app *apperr.Error
	require.ErrorAs(t, err, &app)
	require.Equal(t, apperr.CodeForbidden, app.Code)

	msg, err := svc.Send(ctx, 1, SendInput{ChannelID: 42, ChannelKind: domain.KindGuild, Content: "hello guild", Token: "tok"})
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, 8, msg.ID, "tok"))
	_, err = svc.Send(ctx, 1, SendInput{ChannelID: 43, ChannelKind: domain.KindGuild, Content: "nope", Token: "tok"})
	require.Error(t, err)
}
