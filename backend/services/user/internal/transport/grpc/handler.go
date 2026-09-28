package grpcserver

import (
	"context"

	userv1 "rokko.dev/gen/user/v1"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/id"
	"rokko.dev/services/user/internal/domain"
	"rokko.dev/services/user/internal/service"
)

type Handler struct {
	userv1.UnimplementedUserServiceServer
	svc *service.Service
}

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) GetMe(ctx context.Context, _ *userv1.GetMeRequest) (*userv1.Profile, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.Get(ctx, me)
	if err != nil {
		return nil, err
	}
	return profile(p), nil
}

func (h *Handler) UpdateProfile(ctx context.Context, req *userv1.UpdateProfileRequest) (*userv1.Profile, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.UpdateProfile(ctx, me, service.UpdateInput{
		DisplayName: req.GetDisplayName(), AvatarURL: req.GetAvatarUrl(), StatusText: req.GetStatusText(),
	})
	if err != nil {
		return nil, err
	}
	return profile(p), nil
}

func (h *Handler) GetProfile(ctx context.Context, req *userv1.GetProfileRequest) (*userv1.Profile, error) {
	userID, err := id.ParseID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	p, err := h.svc.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	return profile(p), nil
}

func (h *Handler) SearchUsers(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	users, err := h.svc.Search(ctx, req.GetQuery(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	return &userv1.SearchUsersResponse{Users: profiles(users)}, nil
}

func (h *Handler) SendFriendRequest(ctx context.Context, req *userv1.UserRef) (*userv1.Relationship, error) {
	me, target, err := pair(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	status, err := h.svc.SendFriendRequest(ctx, me, target)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.Get(ctx, target)
	if err != nil {
		return nil, err
	}
	return &userv1.Relationship{User: profile(p), Status: status}, nil
}

func (h *Handler) AcceptFriendRequest(ctx context.Context, req *userv1.UserRef) (*userv1.Relationship, error) {
	me, target, err := pair(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Accept(ctx, me, target); err != nil {
		return nil, err
	}
	p, err := h.svc.Get(ctx, target)
	if err != nil {
		return nil, err
	}
	return &userv1.Relationship{User: profile(p), Status: domain.RelFriend}, nil
}

func (h *Handler) DeclineFriendRequest(ctx context.Context, req *userv1.UserRef) (*userv1.Relationship, error) {
	me, target, err := pair(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Decline(ctx, me, target); err != nil {
		return nil, err
	}
	return &userv1.Relationship{Status: "declined"}, nil
}

func (h *Handler) RemoveFriend(ctx context.Context, req *userv1.UserRef) (*userv1.Empty, error) {
	me, target, err := pair(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.RemoveFriend(ctx, me, target); err != nil {
		return nil, err
	}
	return &userv1.Empty{}, nil
}

func (h *Handler) BlockUser(ctx context.Context, req *userv1.UserRef) (*userv1.Empty, error) {
	me, target, err := pair(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Block(ctx, me, target); err != nil {
		return nil, err
	}
	return &userv1.Empty{}, nil
}

func (h *Handler) UnblockUser(ctx context.Context, req *userv1.UserRef) (*userv1.Empty, error) {
	me, target, err := pair(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Unblock(ctx, me, target); err != nil {
		return nil, err
	}
	return &userv1.Empty{}, nil
}

func (h *Handler) ListFriends(ctx context.Context, _ *userv1.ListRequest) (*userv1.ListRelationshipsResponse, error) {
	return h.list(ctx, domain.RelFriend)
}

func (h *Handler) ListIncomingRequests(ctx context.Context, _ *userv1.ListRequest) (*userv1.ListRelationshipsResponse, error) {
	return h.list(ctx, domain.RelPendingIn)
}

func (h *Handler) ListBlocked(ctx context.Context, _ *userv1.ListRequest) (*userv1.ListRelationshipsResponse, error) {
	return h.list(ctx, domain.RelBlocked)
}

func (h *Handler) IsBlocked(ctx context.Context, req *userv1.IsBlockedRequest) (*userv1.IsBlockedResponse, error) {
	a, err := id.ParseID(req.GetUserA())
	if err != nil {
		return nil, err
	}
	b, err := id.ParseID(req.GetUserB())
	if err != nil {
		return nil, err
	}
	blocked, err := h.svc.IsBlocked(ctx, a, b)
	if err != nil {
		return nil, err
	}
	return &userv1.IsBlockedResponse{Blocked: blocked}, nil
}

func (h *Handler) EnsureProfile(ctx context.Context, req *userv1.EnsureProfileRequest) (*userv1.Profile, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	userID, err := id.ParseID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	if me != userID {
		return nil, apperr.Forbidden("cannot create another profile")
	}
	p, err := h.svc.EnsureProfile(ctx, userID, req.GetUsername())
	if err != nil {
		return nil, err
	}
	return profile(p), nil
}

func (h *Handler) ListFriendIDs(ctx context.Context, _ *userv1.ListRequest) (*userv1.IDList, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := h.svc.FriendIDs(ctx, me)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ids))
	for i, v := range ids {
		out[i] = id.Format(v)
	}
	return &userv1.IDList{Ids: out}, nil
}

func (h *Handler) list(ctx context.Context, rel string) (*userv1.ListRelationshipsResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	users, err := h.svc.List(ctx, me, rel)
	if err != nil {
		return nil, err
	}
	rels := make([]*userv1.Relationship, len(users))
	for i, p := range users {
		rels[i] = &userv1.Relationship{User: profile(p), Status: rel}
	}
	return &userv1.ListRelationshipsResponse{Relationships: rels}, nil
}

func current(ctx context.Context) (int64, error) {
	idnty, ok := authn.FromContext(ctx)
	if !ok {
		return 0, apperr.Unauthorized("missing user")
	}
	return idnty.UserID, nil
}

func pair(ctx context.Context, raw string) (int64, int64, error) {
	me, err := current(ctx)
	if err != nil {
		return 0, 0, err
	}
	target, err := id.ParseID(raw)
	if err != nil {
		return 0, 0, err
	}
	return me, target, nil
}

func profile(p domain.Profile) *userv1.Profile {
	return &userv1.Profile{
		Id: pID(p.ID), Username: p.Username, Discriminator: p.Discriminator,
		DisplayName: p.DisplayName, AvatarUrl: p.AvatarURL, StatusText: p.StatusText,
	}
}

func profiles(in []domain.Profile) []*userv1.Profile {
	out := make([]*userv1.Profile, len(in))
	for i, p := range in {
		out[i] = profile(p)
	}
	return out
}

func pID(v int64) string { return id.Format(v) }
