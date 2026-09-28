package service

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/validate"
	"rokko.dev/services/guild/internal/domain"
)

const inviteAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

type CreateGuildInput struct {
	Name    string `validate:"required,min=2,max=100"`
	IconURL string `validate:"max=512"`
}

type ChannelInput struct {
	Name string `validate:"required,min=1,max=100"`
	Type string `validate:"required"`
}

type InviteInput struct {
	MaxUses   int   `validate:"gte=0,lte=100"`
	ExpiresIn int64 `validate:"gte=0,lte=2592000"`
}

type Service struct {
	store repositoryStore
	ids   *id.Generator
	now   func() time.Time
}

type repositoryStore interface {
	CreateGuild(ctx context.Context, g domain.Guild, channels []domain.Channel) error
	UpdateGuild(ctx context.Context, g domain.Guild) error
	DeleteGuild(ctx context.Context, guildID int64) error
	GetGuild(ctx context.Context, guildID int64) (domain.Guild, error)
	ListGuilds(ctx context.Context, userID int64) ([]domain.Guild, error)
	Role(ctx context.Context, guildID, userID int64) (string, error)
	SaveChannel(ctx context.Context, ch domain.Channel) error
	DeleteChannel(ctx context.Context, channelID int64) error
	Channel(ctx context.Context, channelID int64) (domain.Channel, error)
	Channels(ctx context.Context, guildID int64) ([]domain.Channel, error)
	Reorder(ctx context.Context, guildID int64, ids []int64) error
	SaveInvite(ctx context.Context, inv domain.Invite) error
	ConsumeInvite(ctx context.Context, code string, userID int64, now time.Time) (domain.Guild, error)
	Invites(ctx context.Context, guildID int64) ([]domain.Invite, error)
	GetInvite(ctx context.Context, code string) (domain.Invite, error)
	DeleteInvite(ctx context.Context, code string) error
	RemoveMember(ctx context.Context, guildID, userID int64) error
	SetRole(ctx context.Context, guildID, userID int64, role string) error
	Members(ctx context.Context, guildID int64) ([]domain.Member, error)
}

func New(store repositoryStore, ids *id.Generator) *Service {
	return &Service{store: store, ids: ids, now: time.Now}
}

func (s *Service) CreateGuild(ctx context.Context, ownerID int64, in CreateGuildInput) (domain.Guild, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validate.Struct(in); err != nil {
		return domain.Guild{}, err
	}
	g := domain.Guild{ID: s.ids.Next(), Name: in.Name, IconURL: in.IconURL, OwnerID: ownerID, MyRole: domain.RoleOwner}
	channels := []domain.Channel{
		{ID: s.ids.Next(), GuildID: g.ID, Name: "general", Type: domain.ChannelText, Position: 0},
		{ID: s.ids.Next(), GuildID: g.ID, Name: "General", Type: domain.ChannelVoice, Position: 1},
	}
	if err := s.store.CreateGuild(ctx, g, channels); err != nil {
		return domain.Guild{}, err
	}
	return g, nil
}

func (s *Service) UpdateGuild(ctx context.Context, userID int64, guildID int64, in CreateGuildInput) (domain.Guild, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validate.Struct(in); err != nil {
		return domain.Guild{}, err
	}
	if err := s.require(ctx, guildID, userID, domain.CanManageGuild); err != nil {
		return domain.Guild{}, err
	}
	g := domain.Guild{ID: guildID, Name: in.Name, IconURL: in.IconURL}
	if err := s.store.UpdateGuild(ctx, g); err != nil {
		return domain.Guild{}, err
	}
	got, err := s.store.GetGuild(ctx, guildID)
	if err != nil {
		return domain.Guild{}, err
	}
	got.MyRole, _ = s.store.Role(ctx, guildID, userID)
	return got, nil
}

func (s *Service) DeleteGuild(ctx context.Context, userID, guildID int64) error {
	if err := s.require(ctx, guildID, userID, domain.CanDeleteGuild); err != nil {
		return err
	}
	return s.store.DeleteGuild(ctx, guildID)
}

func (s *Service) GetGuild(ctx context.Context, userID, guildID int64) (domain.Guild, error) {
	role, err := s.store.Role(ctx, guildID, userID)
	if err != nil {
		return domain.Guild{}, err
	}
	g, err := s.store.GetGuild(ctx, guildID)
	if err != nil {
		return domain.Guild{}, err
	}
	g.MyRole = role
	return g, nil
}

func (s *Service) ListMyGuilds(ctx context.Context, userID int64) ([]domain.Guild, error) {
	return s.store.ListGuilds(ctx, userID)
}

func (s *Service) CreateChannel(ctx context.Context, userID int64, guildID int64, in ChannelInput) (domain.Channel, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validate.Struct(in); err != nil {
		return domain.Channel{}, err
	}
	if !domain.ValidChannelType(in.Type) {
		return domain.Channel{}, apperr.Invalid("channel type must be text or voice")
	}
	if err := s.require(ctx, guildID, userID, domain.CanManageChannels); err != nil {
		return domain.Channel{}, err
	}
	existing, err := s.store.Channels(ctx, guildID)
	if err != nil {
		return domain.Channel{}, err
	}
	ch := domain.Channel{ID: s.ids.Next(), GuildID: guildID, Name: in.Name, Type: in.Type, Position: len(existing)}
	if err := s.store.SaveChannel(ctx, ch); err != nil {
		return domain.Channel{}, err
	}
	return ch, nil
}

func (s *Service) UpdateChannel(ctx context.Context, userID, channelID int64, name string) (domain.Channel, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return domain.Channel{}, apperr.Invalid("channel name must be 1-100 characters")
	}
	ch, err := s.store.Channel(ctx, channelID)
	if err != nil {
		return domain.Channel{}, err
	}
	if err := s.require(ctx, ch.GuildID, userID, domain.CanManageChannels); err != nil {
		return domain.Channel{}, err
	}
	ch.Name = name
	if err := s.store.SaveChannel(ctx, ch); err != nil {
		return domain.Channel{}, err
	}
	return ch, nil
}

func (s *Service) DeleteChannel(ctx context.Context, userID, channelID int64) error {
	ch, err := s.store.Channel(ctx, channelID)
	if err != nil {
		return err
	}
	if err := s.require(ctx, ch.GuildID, userID, domain.CanManageChannels); err != nil {
		return err
	}
	return s.store.DeleteChannel(ctx, channelID)
}

func (s *Service) ListChannels(ctx context.Context, userID, guildID int64) ([]domain.Channel, error) {
	if _, err := s.store.Role(ctx, guildID, userID); err != nil {
		return nil, err
	}
	return s.store.Channels(ctx, guildID)
}

func (s *Service) Reorder(ctx context.Context, userID, guildID int64, ids []int64) ([]domain.Channel, error) {
	if err := s.require(ctx, guildID, userID, domain.CanManageChannels); err != nil {
		return nil, err
	}
	if err := s.store.Reorder(ctx, guildID, ids); err != nil {
		return nil, err
	}
	return s.store.Channels(ctx, guildID)
}

func (s *Service) CreateInvite(ctx context.Context, userID, guildID int64, in InviteInput) (domain.Invite, error) {
	if err := validate.Struct(in); err != nil {
		return domain.Invite{}, err
	}
	if _, err := s.store.Role(ctx, guildID, userID); err != nil {
		return domain.Invite{}, err
	}
	inv := domain.Invite{Code: randomCode(8), GuildID: guildID, CreatorID: userID, MaxUses: in.MaxUses}
	if in.ExpiresIn > 0 {
		exp := s.now().Add(time.Duration(in.ExpiresIn) * time.Second)
		inv.ExpiresAt = &exp
	}
	if err := s.store.SaveInvite(ctx, inv); err != nil {
		return domain.Invite{}, err
	}
	return inv, nil
}

func (s *Service) Join(ctx context.Context, userID int64, code string) (domain.Guild, error) {
	if strings.TrimSpace(code) == "" {
		return domain.Guild{}, apperr.Invalid("invite code required")
	}
	return s.store.ConsumeInvite(ctx, code, userID, s.now())
}

func (s *Service) ListInvites(ctx context.Context, userID, guildID int64) ([]domain.Invite, error) {
	if err := s.require(ctx, guildID, userID, domain.CanManageGuild); err != nil {
		return nil, err
	}
	return s.store.Invites(ctx, guildID)
}

func (s *Service) RevokeInvite(ctx context.Context, userID int64, code string) error {
	inv, err := s.store.GetInvite(ctx, code)
	if err != nil {
		return err
	}
	if err := s.require(ctx, inv.GuildID, userID, domain.CanManageGuild); err != nil {
		return err
	}
	return s.store.DeleteInvite(ctx, code)
}

func (s *Service) Leave(ctx context.Context, userID, guildID int64) error {
	role, err := s.store.Role(ctx, guildID, userID)
	if err != nil {
		return err
	}
	if role == domain.RoleOwner {
		return apperr.Invalid("owner cannot leave; delete the server instead")
	}
	return s.store.RemoveMember(ctx, guildID, userID)
}

func (s *Service) Kick(ctx context.Context, actor, guildID, target int64) error {
	if actor == target {
		return apperr.Invalid("cannot kick yourself")
	}
	actorRole, err := s.store.Role(ctx, guildID, actor)
	if err != nil {
		return err
	}
	targetRole, err := s.store.Role(ctx, guildID, target)
	if err != nil {
		return apperr.NotFound("member not found")
	}
	if !domain.CanKick(actorRole, targetRole) {
		return apperr.Forbidden("cannot kick this member")
	}
	return s.store.RemoveMember(ctx, guildID, target)
}

func (s *Service) SetRole(ctx context.Context, actor, guildID, target int64, role string) (domain.Member, error) {
	actorRole, err := s.store.Role(ctx, guildID, actor)
	if err != nil {
		return domain.Member{}, err
	}
	targetRole, err := s.store.Role(ctx, guildID, target)
	if err != nil {
		return domain.Member{}, apperr.NotFound("member not found")
	}
	if !domain.CanSetRole(actorRole, targetRole, role) {
		return domain.Member{}, apperr.Forbidden("cannot change this role")
	}
	if err := s.store.SetRole(ctx, guildID, target, role); err != nil {
		return domain.Member{}, err
	}
	return domain.Member{GuildID: guildID, UserID: target, Role: role}, nil
}

func (s *Service) Members(ctx context.Context, userID, guildID int64) ([]domain.Member, error) {
	if _, err := s.store.Role(ctx, guildID, userID); err != nil {
		return nil, err
	}
	return s.store.Members(ctx, guildID)
}

type Access struct {
	Allowed     bool
	GuildID     int64
	Role        string
	CanModerate bool
	ChannelType string
}

func (s *Service) CheckChannelAccess(ctx context.Context, userID, channelID int64) (Access, error) {
	ch, err := s.store.Channel(ctx, channelID)
	if err != nil {
		return Access{}, err
	}
	role, err := s.store.Role(ctx, ch.GuildID, userID)
	if err != nil {
		return Access{GuildID: ch.GuildID, ChannelType: ch.Type}, nil
	}
	return Access{
		Allowed: true, GuildID: ch.GuildID, Role: role,
		CanModerate: domain.CanModerate(role), ChannelType: ch.Type,
	}, nil
}

func (s *Service) require(ctx context.Context, guildID, userID int64, allow func(string) bool) error {
	role, err := s.store.Role(ctx, guildID, userID)
	if err != nil {
		return err
	}
	if !allow(role) {
		return apperr.Forbidden("missing permission")
	}
	return nil
}

func randomCode(n int) string {
	buf := make([]byte, n)
	rand.Read(buf)
	out := make([]byte, n)
	for i := range buf {
		out[i] = inviteAlphabet[int(buf[i])%len(inviteAlphabet)]
	}
	return string(out)
}
