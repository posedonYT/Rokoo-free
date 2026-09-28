package service

import (
	"context"
	"strings"
	"time"

	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/services/message/internal/domain"
	"rokko.dev/services/message/internal/repository"
)

type GuildAccess interface {
	Check(ctx context.Context, token string, channelID int64) (guildID int64, channelType string, canModerate bool, allowed bool, err error)
}

type Blocks interface {
	IsBlocked(ctx context.Context, token string, a, b int64) (bool, error)
}

type Service struct {
	messages repository.MessageRepository
	dms      repository.DMRepository
	guilds   GuildAccess
	blocks   Blocks
	ids      *id.Generator
	now      func() time.Time
}

func New(messages repository.MessageRepository, dms repository.DMRepository, guilds GuildAccess, blocks Blocks, ids *id.Generator) *Service {
	return &Service{messages: messages, dms: dms, guilds: guilds, blocks: blocks, ids: ids, now: time.Now}
}

func (s *Service) OpenDM(ctx context.Context, me, other int64, token string) (domain.DMChannel, error) {
	if me == other {
		return domain.DMChannel{}, apperr.Invalid("cannot open a dm with yourself")
	}
	if err := s.ensureNotBlocked(ctx, token, me, other); err != nil {
		return domain.DMChannel{}, err
	}
	if existing, ok, err := s.dms.FindPair(ctx, me, other); err != nil || ok {
		return existing, err
	}
	ch := domain.DMChannel{ID: s.ids.Next(), Kind: domain.KindDM, Members: []int64{me, other}}
	if err := s.dms.CreateDM(ctx, ch); err != nil {
		if app, ok := apperr.As(err); ok && app.Code == apperr.CodeConflict {
			existing, ok, findErr := s.dms.FindPair(ctx, me, other)
			if findErr != nil || ok {
				return existing, findErr
			}
		}
		return domain.DMChannel{}, err
	}
	return ch, nil
}

func (s *Service) CreateGroup(ctx context.Context, me int64, others []int64, token string) (domain.DMChannel, error) {
	members := unique(append([]int64{me}, others...))
	if len(members) < 3 || len(members) > domain.MaxGroup {
		return domain.DMChannel{}, apperr.Invalid("group dm must have 3 to 10 members")
	}
	for _, other := range members {
		if other == me {
			continue
		}
		if err := s.ensureNotBlocked(ctx, token, me, other); err != nil {
			return domain.DMChannel{}, err
		}
	}
	ch := domain.DMChannel{ID: s.ids.Next(), Kind: domain.KindGroup, Members: members}
	if err := s.dms.CreateDM(ctx, ch); err != nil {
		return domain.DMChannel{}, err
	}
	return ch, nil
}

func (s *Service) ListDMs(ctx context.Context, me int64) ([]domain.DMChannel, error) {
	return s.dms.ListDMs(ctx, me)
}

func (s *Service) AddGroupMember(ctx context.Context, me, channelID, userID int64, token string) (domain.DMChannel, error) {
	ch, err := s.requireDMMember(ctx, channelID, me)
	if err != nil {
		return domain.DMChannel{}, err
	}
	if ch.Kind != domain.KindGroup {
		return domain.DMChannel{}, apperr.Invalid("not a group dm")
	}
	if len(ch.Members) >= domain.MaxGroup {
		return domain.DMChannel{}, apperr.Invalid("group is full")
	}
	if err := s.ensureNotBlocked(ctx, token, me, userID); err != nil {
		return domain.DMChannel{}, err
	}
	if err := s.dms.AddMember(ctx, channelID, userID); err != nil {
		return domain.DMChannel{}, err
	}
	return s.dms.DM(ctx, channelID)
}

func (s *Service) LeaveGroup(ctx context.Context, me, channelID int64) error {
	ch, err := s.requireDMMember(ctx, channelID, me)
	if err != nil {
		return err
	}
	if ch.Kind != domain.KindGroup {
		return apperr.Invalid("not a group dm")
	}
	return s.dms.RemoveMember(ctx, channelID, me)
}

type SendInput struct {
	ChannelID   int64
	ChannelKind string
	Content     string
	Attachments []domain.Attachment
	Token       string
}

func (s *Service) Send(ctx context.Context, me int64, in SendInput) (domain.Message, error) {
	in.Content = strings.TrimSpace(in.Content)
	if len(in.Content) > domain.MaxLen {
		return domain.Message{}, apperr.Invalid("message is too long")
	}
	if in.Content == "" && len(in.Attachments) == 0 {
		return domain.Message{}, apperr.Invalid("empty message")
	}
	recipients, guildID, err := s.authorizeSend(ctx, me, in)
	if err != nil {
		return domain.Message{}, err
	}
	msg := domain.Message{
		ID: s.ids.Next(), ChannelID: in.ChannelID, ChannelKind: in.ChannelKind,
		AuthorID: me, Content: in.Content, CreatedAt: s.now().Unix(), Attachments: in.Attachments,
	}
	for i := range msg.Attachments {
		if msg.Attachments[i].ID == 0 {
			msg.Attachments[i].ID = s.ids.Next()
		}
	}
	event, err := s.event("message.created", msg, recipients, guildID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := s.messages.InsertMessage(ctx, msg, event); err != nil {
		return domain.Message{}, err
	}
	return msg, nil
}

func (s *Service) Edit(ctx context.Context, me, messageID int64, content, token string) (domain.Message, error) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > domain.MaxLen {
		return domain.Message{}, apperr.Invalid("content must be 1-4000 characters")
	}
	msg, err := s.messages.GetMessage(ctx, messageID)
	if err != nil {
		return domain.Message{}, err
	}
	if msg.AuthorID != me {
		return domain.Message{}, apperr.Forbidden("only the author can edit")
	}
	msg.Content = content
	msg.EditedAt = s.now().Unix()
	recipients, guildID, err := s.fanout(ctx, token, msg)
	if err != nil {
		return domain.Message{}, err
	}
	event, err := s.event("message.updated", msg, recipients, guildID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := s.messages.UpdateMessage(ctx, msg, event); err != nil {
		return domain.Message{}, err
	}
	return msg, nil
}

func (s *Service) Delete(ctx context.Context, me, messageID int64, token string) error {
	msg, err := s.messages.GetMessage(ctx, messageID)
	if err != nil {
		return err
	}
	if msg.AuthorID != me {
		if msg.ChannelKind != domain.KindGuild {
			return apperr.Forbidden("only the author can delete")
		}
		_, _, canModerate, allowed, err := s.guilds.Check(ctx, token, msg.ChannelID)
		if err != nil {
			return err
		}
		if !allowed || !canModerate {
			return apperr.Forbidden("only the author can delete")
		}
	}
	recipients, guildID, err := s.fanout(ctx, token, msg)
	if err != nil {
		return err
	}
	event, err := s.event("message.deleted", msg, recipients, guildID)
	if err != nil {
		return err
	}
	return s.messages.SoftDelete(ctx, messageID, event)
}

func (s *Service) List(ctx context.Context, me int64, channelID int64, kind, token string, before int64, limit int) ([]domain.Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if _, _, err := s.authorizeRead(ctx, me, channelID, kind, token); err != nil {
		return nil, err
	}
	msgs, err := s.messages.ListMessages(ctx, channelID, before, limit)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (s *Service) CheckDM(ctx context.Context, userID, channelID int64) (domain.DMChannel, bool, error) {
	ch, err := s.dms.DM(ctx, channelID)
	if err != nil {
		return domain.DMChannel{}, false, err
	}
	return ch, contains(ch.Members, userID), nil
}

func (s *Service) authorizeSend(ctx context.Context, me int64, in SendInput) ([]int64, int64, error) {
	switch in.ChannelKind {
	case domain.KindGuild:
		guildID, channelType, _, allowed, err := s.guilds.Check(ctx, in.Token, in.ChannelID)
		if err != nil {
			return nil, 0, err
		}
		if !allowed {
			return nil, 0, apperr.Forbidden("no access to channel")
		}
		if channelType != "text" {
			return nil, 0, apperr.Invalid("cannot send messages to a voice channel")
		}
		return nil, guildID, nil
	case domain.KindDM, domain.KindGroup:
		ch, err := s.requireDMMember(ctx, in.ChannelID, me)
		if err != nil {
			return nil, 0, err
		}
		for _, member := range ch.Members {
			if member == me {
				continue
			}
			if err := s.ensureNotBlocked(ctx, in.Token, me, member); err != nil {
				return nil, 0, err
			}
		}
		return ch.Members, 0, nil
	default:
		return nil, 0, apperr.Invalid("unknown channel kind")
	}
}

func (s *Service) authorizeRead(ctx context.Context, me, channelID int64, kind, token string) ([]int64, int64, error) {
	return s.authorizeSend(ctx, me, SendInput{ChannelID: channelID, ChannelKind: kind, Token: token, Content: "x"})
}

func (s *Service) requireDMMember(ctx context.Context, channelID, userID int64) (domain.DMChannel, error) {
	ch, err := s.dms.DM(ctx, channelID)
	if err != nil {
		return domain.DMChannel{}, err
	}
	if !contains(ch.Members, userID) {
		return domain.DMChannel{}, apperr.Forbidden("not a member of this dm")
	}
	return ch, nil
}

func (s *Service) ensureNotBlocked(ctx context.Context, token string, a, b int64) error {
	if s.blocks == nil {
		return nil
	}
	blocked, err := s.blocks.IsBlocked(ctx, token, a, b)
	if err != nil {
		return err
	}
	if blocked {
		return apperr.Forbidden("cannot message a blocked user")
	}
	return nil
}

func (s *Service) fanout(ctx context.Context, token string, msg domain.Message) ([]int64, int64, error) {
	if msg.ChannelKind == domain.KindGuild {
		guildID, _, _, _, err := s.guilds.Check(ctx, token, msg.ChannelID)
		return nil, guildID, err
	}
	ch, err := s.dms.DM(ctx, msg.ChannelID)
	if err != nil {
		return nil, 0, err
	}
	return ch.Members, 0, nil
}

func (s *Service) event(typ string, msg domain.Message, recipients []int64, guildID int64) (domain.Event, error) {
	eventID := s.ids.Next()
	env, err := eventx.New(id.Format(eventID), typ, map[string]any{
		"id": id.Format(msg.ID), "channel_id": id.Format(msg.ChannelID),
		"channel_kind": msg.ChannelKind, "author_id": id.Format(msg.AuthorID), "content": msg.Content,
	})
	if err != nil {
		return domain.Event{}, err
	}
	env.ChannelID = id.Format(msg.ChannelID)
	if guildID != 0 && msg.ChannelKind == domain.KindGuild {
		env.GuildID = id.Format(guildID)
	}
	env.RecipientIDs = formatIDs(recipients)
	raw, err := env.Bytes()
	if err != nil {
		return domain.Event{}, err
	}
	return domain.Event{ID: eventID, Topic: domain.Topic, Key: id.Format(msg.ChannelID), Payload: raw}, nil
}

func unique(ids []int64) []int64 {
	seen := map[int64]struct{}{}
	var out []int64
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func contains(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func formatIDs(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, v := range ids {
		out = append(out, id.Format(v))
	}
	return out
}
