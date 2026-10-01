package grpcserver

import (
	"context"
	"net"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

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
	out, err := h.svc.Register(ctx, service.RegisterInput{Email: req.GetEmail(), Username: req.GetUsername(), Password: req.GetPassword(), IP: clientIP(ctx)})
	if err != nil {
		return nil, err
	}
	return toAuth(out), nil
}

func (h *Handler) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.AuthResponse, error) {
	out, err := h.svc.Login(ctx, service.LoginInput{Email: req.GetEmail(), Password: req.GetPassword(), IP: clientIP(ctx)})
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

// clientIP returns the caller IP. Envoy (use_remote_address: true) appends the real peer address
// to the right of x-forwarded-for, so the rightmost entry is the one that cannot be spoofed by the client.
func clientIP(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("x-forwarded-for"); len(v) > 0 {
			parts := strings.Split(strings.Join(v, ","), ",")
			for i := len(parts) - 1; i >= 0; i-- {
				if ip := parseIP(parts[i]); ip != "" {
					return ip
				}
			}
		}
		if v := md.Get("x-real-ip"); len(v) > 0 {
			if ip := parseIP(v[0]); ip != "" {
				return ip
			}
		}
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return parseIP(p.Addr.String())
	}
	return ""
}

func parseIP(s string) string {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	if ip := net.ParseIP(strings.Trim(s, "[]")); ip != nil {
		return ip.String()
	}
	return ""
}
