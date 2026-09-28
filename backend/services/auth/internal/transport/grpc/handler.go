package grpcserver

import (
	"context"

	authv1 "rokko.dev/gen/auth/v1"
	"rokko.dev/pkg/id"
	"rokko.dev/services/auth/internal/service"
)

type Handler struct {
	authv1.UnimplementedAuthServiceServer
	svc *service.Service
}

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.AuthResponse, error) {
	out, err := h.svc.Register(ctx, service.RegisterInput{Email: req.GetEmail(), Username: req.GetUsername(), Password: req.GetPassword()})
	if err != nil {
		return nil, err
	}
	return toAuth(out), nil
}

func (h *Handler) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.AuthResponse, error) {
	out, err := h.svc.Login(ctx, service.LoginInput{Email: req.GetEmail(), Password: req.GetPassword()})
	if err != nil {
		return nil, err
	}
	return toAuth(out), nil
}

func (h *Handler) Refresh(ctx context.Context, req *authv1.RefreshRequest) (*authv1.AuthResponse, error) {
	out, err := h.svc.Refresh(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, err
	}
	return toAuth(out), nil
}

func (h *Handler) Logout(ctx context.Context, req *authv1.LogoutRequest) (*authv1.LogoutResponse, error) {
	if err := h.svc.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, err
	}
	return &authv1.LogoutResponse{}, nil
}

func toAuth(out service.Tokens) *authv1.AuthResponse {
	return &authv1.AuthResponse{
		UserId:       id.Format(out.UserID),
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		Username:     out.Username,
	}
}
