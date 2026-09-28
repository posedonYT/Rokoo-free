package grpcx

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/authn"
)

func NewServer(log *zap.Logger, tokens *authn.Manager, public map[string]struct{}) *grpc.Server {
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			RecoveryUnary(log),
			LoggingUnary(log),
			AuthUnary(tokens, public),
		),
	)
}

func RecoveryUnary(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic", zap.Any("recover", r), zap.String("method", info.FullMethod))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func LoggingUnary(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)
		log.Info("grpc",
			zap.String("method", info.FullMethod),
			zap.String("code", code.String()),
			zap.Duration("dur", time.Since(start)),
		)
		if err != nil {
			return nil, apperr.GRPCStatus(err)
		}
		return resp, nil
	}
}

func AuthUnary(tokens *authn.Manager, public map[string]struct{}) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := public[info.FullMethod]; ok {
			return handler(ctx, req)
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		raw := ""
		if vals := md.Get("authorization"); len(vals) > 0 {
			raw = strings.TrimSpace(vals[0])
		}
		if !strings.HasPrefix(raw, "Bearer ") {
			return nil, status.Error(codes.Unauthenticated, "missing bearer token")
		}
		token := strings.TrimPrefix(raw, "Bearer ")
		id, err := tokens.Parse(token)
		if err != nil {
			return nil, apperr.GRPCStatus(err)
		}
		return handler(authn.WithIdentity(ctx, id), req)
	}
}

func Dial(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecureCreds()))
}

func insecureCreds() credentials.TransportCredentials {
	return insecure.NewCredentials()
}

func WithToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}
