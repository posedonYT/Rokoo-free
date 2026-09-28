package grpcserver

import (
	"context"

	guildv1 "rokko.dev/gen/guild/v1"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/id"
	"rokko.dev/services/guild/internal/domain"
	"rokko.dev/services/guild/internal/service"
)

type Handler struct {
	guildv1.UnimplementedGuildServiceServer
	svc *service.Service
}

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) CreateGuild(ctx context.Context, req *guildv1.CreateGuildRequest) (*guildv1.Guild, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	g, err := h.svc.CreateGuild(ctx, me, service.CreateGuildInput{Name: req.GetName(), IconURL: req.GetIconUrl()})
	if err != nil {
		return nil, err
	}
	return guild(g), nil
}

func (h *Handler) UpdateGuild(ctx context.Context, req *guildv1.UpdateGuildRequest) (*guildv1.Guild, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	g, err := h.svc.UpdateGuild(ctx, me, guildID, service.CreateGuildInput{Name: req.GetName(), IconURL: req.GetIconUrl()})
	if err != nil {
		return nil, err
	}
	return guild(g), nil
}

func (h *Handler) DeleteGuild(ctx context.Context, req *guildv1.GuildRef) (*guildv1.Empty, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteGuild(ctx, me, guildID); err != nil {
		return nil, err
	}
	return &guildv1.Empty{}, nil
}

func (h *Handler) GetGuild(ctx context.Context, req *guildv1.GuildRef) (*guildv1.Guild, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	g, err := h.svc.GetGuild(ctx, me, guildID)
	if err != nil {
		return nil, err
	}
	return guild(g), nil
}

func (h *Handler) ListMyGuilds(ctx context.Context, _ *guildv1.Empty) (*guildv1.ListGuildsResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	list, err := h.svc.ListMyGuilds(ctx, me)
	if err != nil {
		return nil, err
	}
	out := make([]*guildv1.Guild, len(list))
	for i, g := range list {
		out[i] = guild(g)
	}
	return &guildv1.ListGuildsResponse{Guilds: out}, nil
}

func (h *Handler) CreateChannel(ctx context.Context, req *guildv1.CreateChannelRequest) (*guildv1.Channel, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	ch, err := h.svc.CreateChannel(ctx, me, guildID, service.ChannelInput{Name: req.GetName(), Type: req.GetType()})
	if err != nil {
		return nil, err
	}
	return channel(ch), nil
}

func (h *Handler) UpdateChannel(ctx context.Context, req *guildv1.UpdateChannelRequest) (*guildv1.Channel, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	ch, err := h.svc.UpdateChannel(ctx, me, channelID, req.GetName())
	if err != nil {
		return nil, err
	}
	return channel(ch), nil
}

func (h *Handler) DeleteChannel(ctx context.Context, req *guildv1.ChannelRef) (*guildv1.Empty, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteChannel(ctx, me, channelID); err != nil {
		return nil, err
	}
	return &guildv1.Empty{}, nil
}

func (h *Handler) ListChannels(ctx context.Context, req *guildv1.GuildRef) (*guildv1.ListChannelsResponse, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	list, err := h.svc.ListChannels(ctx, me, guildID)
	if err != nil {
		return nil, err
	}
	return &guildv1.ListChannelsResponse{Channels: channels(list)}, nil
}

func (h *Handler) ReorderChannels(ctx context.Context, req *guildv1.ReorderChannelsRequest) (*guildv1.ListChannelsResponse, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(req.GetChannelIds()))
	for i, raw := range req.GetChannelIds() {
		ids[i], err = id.ParseID(raw)
		if err != nil {
			return nil, err
		}
	}
	list, err := h.svc.Reorder(ctx, me, guildID, ids)
	if err != nil {
		return nil, err
	}
	return &guildv1.ListChannelsResponse{Channels: channels(list)}, nil
}

func (h *Handler) CreateInvite(ctx context.Context, req *guildv1.CreateInviteRequest) (*guildv1.Invite, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	inv, err := h.svc.CreateInvite(ctx, me, guildID, service.InviteInput{MaxUses: int(req.GetMaxUses()), ExpiresIn: req.GetExpiresInSeconds()})
	if err != nil {
		return nil, err
	}
	return invite(inv), nil
}

func (h *Handler) JoinByInvite(ctx context.Context, req *guildv1.JoinByInviteRequest) (*guildv1.Guild, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	g, err := h.svc.Join(ctx, me, req.GetCode())
	if err != nil {
		return nil, err
	}
	return guild(g), nil
}

func (h *Handler) ListInvites(ctx context.Context, req *guildv1.GuildRef) (*guildv1.ListInvitesResponse, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	list, err := h.svc.ListInvites(ctx, me, guildID)
	if err != nil {
		return nil, err
	}
	out := make([]*guildv1.Invite, len(list))
	for i, inv := range list {
		out[i] = invite(inv)
	}
	return &guildv1.ListInvitesResponse{Invites: out}, nil
}

func (h *Handler) RevokeInvite(ctx context.Context, req *guildv1.RevokeInviteRequest) (*guildv1.Empty, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.RevokeInvite(ctx, me, req.GetCode()); err != nil {
		return nil, err
	}
	return &guildv1.Empty{}, nil
}

func (h *Handler) LeaveGuild(ctx context.Context, req *guildv1.GuildRef) (*guildv1.Empty, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Leave(ctx, me, guildID); err != nil {
		return nil, err
	}
	return &guildv1.Empty{}, nil
}

func (h *Handler) KickMember(ctx context.Context, req *guildv1.KickMemberRequest) (*guildv1.Empty, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	target, err := id.ParseID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Kick(ctx, me, guildID, target); err != nil {
		return nil, err
	}
	return &guildv1.Empty{}, nil
}

func (h *Handler) SetMemberRole(ctx context.Context, req *guildv1.SetMemberRoleRequest) (*guildv1.Member, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	target, err := id.ParseID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	member, err := h.svc.SetRole(ctx, me, guildID, target, req.GetRole())
	if err != nil {
		return nil, err
	}
	return &guildv1.Member{UserId: id.Format(member.UserID), Role: member.Role}, nil
}

func (h *Handler) ListMembers(ctx context.Context, req *guildv1.GuildRef) (*guildv1.ListMembersResponse, error) {
	me, guildID, err := meAndGuild(ctx, req.GetGuildId())
	if err != nil {
		return nil, err
	}
	list, err := h.svc.Members(ctx, me, guildID)
	if err != nil {
		return nil, err
	}
	out := make([]*guildv1.Member, len(list))
	for i, m := range list {
		out[i] = &guildv1.Member{UserId: id.Format(m.UserID), Role: m.Role}
	}
	return &guildv1.ListMembersResponse{Members: out}, nil
}

func (h *Handler) CheckChannelAccess(ctx context.Context, req *guildv1.CheckChannelAccessRequest) (*guildv1.CheckChannelAccessResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	access, err := h.svc.CheckChannelAccess(ctx, me, channelID)
	if err != nil {
		return nil, err
	}
	return &guildv1.CheckChannelAccessResponse{
		Allowed: access.Allowed, GuildId: id.Format(access.GuildID), Role: access.Role,
		CanModerate: access.CanModerate, ChannelType: access.ChannelType,
	}, nil
}

func current(ctx context.Context) (int64, error) {
	idnty, ok := authn.FromContext(ctx)
	if !ok {
		return 0, apperr.Unauthorized("missing user")
	}
	return idnty.UserID, nil
}

func meAndGuild(ctx context.Context, raw string) (int64, int64, error) {
	me, err := current(ctx)
	if err != nil {
		return 0, 0, err
	}
	guildID, err := id.ParseID(raw)
	if err != nil {
		return 0, 0, err
	}
	return me, guildID, nil
}

func guild(g domain.Guild) *guildv1.Guild {
	return &guildv1.Guild{Id: id.Format(g.ID), Name: g.Name, IconUrl: g.IconURL, OwnerId: id.Format(g.OwnerID), MyRole: g.MyRole}
}

func channel(ch domain.Channel) *guildv1.Channel {
	return &guildv1.Channel{Id: id.Format(ch.ID), GuildId: id.Format(ch.GuildID), Name: ch.Name, Type: ch.Type, Position: int32(ch.Position)}
}

func channels(list []domain.Channel) []*guildv1.Channel {
	out := make([]*guildv1.Channel, len(list))
	for i, ch := range list {
		out[i] = channel(ch)
	}
	return out
}

func invite(inv domain.Invite) *guildv1.Invite {
	var exp int64
	if inv.ExpiresAt != nil {
		exp = inv.ExpiresAt.Unix()
	}
	return &guildv1.Invite{
		Code: inv.Code, GuildId: id.Format(inv.GuildID), CreatorId: id.Format(inv.CreatorID),
		MaxUses: int32(inv.MaxUses), Uses: int32(inv.Uses), ExpiresAtUnix: exp,
	}
}
