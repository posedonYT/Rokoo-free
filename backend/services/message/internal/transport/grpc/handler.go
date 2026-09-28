package grpcserver

import (
	"context"

	messagev1 "rokko.dev/gen/message/v1"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/id"
	"rokko.dev/services/message/internal/domain"
	"rokko.dev/services/message/internal/service"
)

type Handler struct {
	messagev1.UnimplementedMessageServiceServer
	svc *service.Service
}

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) OpenDM(ctx context.Context, req *messagev1.OpenDMRequest) (*messagev1.DMChannel, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	other, err := id.ParseID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	ch, err := h.svc.OpenDM(ctx, me, other, bearer(ctx))
	if err != nil {
		return nil, err
	}
	return dm(ch), nil
}

func (h *Handler) CreateGroupDM(ctx context.Context, req *messagev1.CreateGroupDMRequest) (*messagev1.DMChannel, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	others := make([]int64, len(req.GetUserIds()))
	for i, raw := range req.GetUserIds() {
		others[i], err = id.ParseID(raw)
		if err != nil {
			return nil, err
		}
	}
	ch, err := h.svc.CreateGroup(ctx, me, others, bearer(ctx))
	if err != nil {
		return nil, err
	}
	return dm(ch), nil
}

func (h *Handler) ListDMs(ctx context.Context, _ *messagev1.ListDMsRequest) (*messagev1.ListDMsResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	list, err := h.svc.ListDMs(ctx, me)
	if err != nil {
		return nil, err
	}
	out := make([]*messagev1.DMChannel, len(list))
	for i, ch := range list {
		out[i] = dm(ch)
	}
	return &messagev1.ListDMsResponse{Channels: out}, nil
}

func (h *Handler) AddGroupMember(ctx context.Context, req *messagev1.GroupMemberRequest) (*messagev1.DMChannel, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	userID, err := id.ParseID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	ch, err := h.svc.AddGroupMember(ctx, me, channelID, userID, bearer(ctx))
	if err != nil {
		return nil, err
	}
	return dm(ch), nil
}

func (h *Handler) LeaveGroupDM(ctx context.Context, req *messagev1.LeaveGroupRequest) (*messagev1.Empty, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.LeaveGroup(ctx, me, channelID); err != nil {
		return nil, err
	}
	return &messagev1.Empty{}, nil
}

func (h *Handler) SendMessage(ctx context.Context, req *messagev1.SendMessageRequest) (*messagev1.Message, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	msg, err := h.svc.Send(ctx, me, service.SendInput{
		ChannelID: channelID, ChannelKind: req.GetChannelKind(), Content: req.GetContent(),
		Attachments: attachmentsIn(req.GetAttachments()), Token: bearer(ctx),
	})
	if err != nil {
		return nil, err
	}
	return message(msg), nil
}

func (h *Handler) EditMessage(ctx context.Context, req *messagev1.EditMessageRequest) (*messagev1.Message, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	messageID, err := id.ParseID(req.GetMessageId())
	if err != nil {
		return nil, err
	}
	msg, err := h.svc.Edit(ctx, me, messageID, req.GetContent(), bearer(ctx))
	if err != nil {
		return nil, err
	}
	return message(msg), nil
}

func (h *Handler) DeleteMessage(ctx context.Context, req *messagev1.DeleteMessageRequest) (*messagev1.Empty, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	messageID, err := id.ParseID(req.GetMessageId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(ctx, me, messageID, bearer(ctx)); err != nil {
		return nil, err
	}
	return &messagev1.Empty{}, nil
}

func (h *Handler) ListMessages(ctx context.Context, req *messagev1.ListMessagesRequest) (*messagev1.ListMessagesResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	var before int64
	if req.GetBefore() != "" {
		before, err = id.ParseID(req.GetBefore())
		if err != nil {
			return nil, err
		}
	}
	list, err := h.svc.List(ctx, me, channelID, req.GetChannelKind(), bearer(ctx), before, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := make([]*messagev1.Message, len(list))
	for i, msg := range list {
		out[i] = message(msg)
	}
	return &messagev1.ListMessagesResponse{Messages: out}, nil
}

func (h *Handler) CheckDMAccess(ctx context.Context, req *messagev1.CheckDMAccessRequest) (*messagev1.CheckDMAccessResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	ch, allowed, err := h.svc.CheckDM(ctx, me, channelID)
	if err != nil {
		return nil, err
	}
	return &messagev1.CheckDMAccessResponse{Allowed: allowed, MemberIds: formatIDs(ch.Members), Kind: ch.Kind}, nil
}

func (h *Handler) ListDMMemberIDs(ctx context.Context, req *messagev1.DMRef) (*messagev1.IDList, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	channelID, err := id.ParseID(req.GetChannelId())
	if err != nil {
		return nil, err
	}
	ch, allowed, err := h.svc.CheckDM(ctx, me, channelID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, apperr.Forbidden("not a member of this dm")
	}
	return &messagev1.IDList{Ids: formatIDs(ch.Members)}, nil
}

func current(ctx context.Context) (int64, error) {
	idnty, ok := authn.FromContext(ctx)
	if !ok {
		return 0, apperr.Unauthorized("missing user")
	}
	return idnty.UserID, nil
}

func bearer(ctx context.Context) string {
	idnty, _ := authn.FromContext(ctx)
	return idnty.Token
}

func dm(ch domain.DMChannel) *messagev1.DMChannel {
	return &messagev1.DMChannel{Id: id.Format(ch.ID), Kind: ch.Kind, MemberIds: formatIDs(ch.Members)}
}

func message(msg domain.Message) *messagev1.Message {
	return &messagev1.Message{
		Id: id.Format(msg.ID), ChannelId: id.Format(msg.ChannelID), ChannelKind: msg.ChannelKind,
		AuthorId: id.Format(msg.AuthorID), Content: msg.Content, CreatedAtUnix: msg.CreatedAt, EditedAtUnix: msg.EditedAt,
		Attachments: attachmentsOut(msg.Attachments),
	}
}

func attachmentsIn(in []*messagev1.Attachment) []domain.Attachment {
	out := make([]domain.Attachment, len(in))
	for i, att := range in {
		out[i] = domain.Attachment{MediaID: att.GetMediaId(), Filename: att.GetFilename(), ContentType: att.GetContentType(), Size: att.GetSize()}
	}
	return out
}

func attachmentsOut(in []domain.Attachment) []*messagev1.Attachment {
	out := make([]*messagev1.Attachment, len(in))
	for i, att := range in {
		out[i] = &messagev1.Attachment{MediaId: att.MediaID, Filename: att.Filename, ContentType: att.ContentType, Size: att.Size}
	}
	return out
}

func formatIDs(ids []int64) []string {
	out := make([]string, len(ids))
	for i, v := range ids {
		out[i] = id.Format(v)
	}
	return out
}
