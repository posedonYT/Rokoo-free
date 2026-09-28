package main

import (
	"context"
	"flag"
	"net"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	authv1 "rokko.dev/gen/auth/v1"
	userv1 "rokko.dev/gen/user/v1"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/config"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/kafkax"
	"rokko.dev/pkg/logger"
	"rokko.dev/pkg/migratex"
	"rokko.dev/pkg/outbox"
	"rokko.dev/pkg/postgres"
	"rokko.dev/pkg/redisx"
	authcfg "rokko.dev/services/auth/internal/config"
	"rokko.dev/services/auth/internal/repository"
	"rokko.dev/services/auth/internal/service"
	grpcserver "rokko.dev/services/auth/internal/transport/grpc"
	"rokko.dev/services/auth/internal/userclient"
	"rokko.dev/services/auth/migrations"
)

func main() {
	path := flag.String("config", "config.yaml", "config file")
	flag.Parse()
	var cfg authcfg.Config
	if err := config.Load(*path, &cfg); err != nil {
		panic(err)
	}
	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		panic(err)
	}
	defer log.Sync()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := migratex.Up(cfg.PostgresDSN, migrations.FS); err != nil {
		log.Fatal("migrate", zap.Error(err))
	}
	pool, err := postgres.NewPool(ctx, postgres.Config{DSN: cfg.PostgresDSN, ApplicationName: "auth"})
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pool.Close()
	rdb := redisx.New(redisx.Config{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	if err := redisx.Ping(ctx, rdb); err != nil {
		log.Fatal("redis", zap.Error(err))
	}
	defer rdb.Close()
	tokens, err := authn.Load(cfg.PrivateKeyPath, cfg.PublicKeyPath, cfg.Issuer, cfg.AccessTTL)
	if err != nil {
		log.Fatal("keys", zap.Error(err))
	}
	writer := kafkax.NewWriter(cfg.KafkaBrokers)
	defer writer.Close()
	go outbox.RunRelayLogged(ctx, pool, writer, func(err error) { log.Warn("outbox", zap.Error(err)) })
	var profiles service.Profiles
	if cfg.UserServiceAddr != "" {
		conn, err := grpcx.Dial(cfg.UserServiceAddr)
		if err != nil {
			log.Fatal("user dial", zap.Error(err))
		}
		defer conn.Close()
		profiles = userclient.New(userv1.NewUserServiceClient(conn))
	}
	svc := service.New(repository.NewPostgres(pool), repository.NewRedisRefresh(rdb), tokens, id.New(cfg.WorkerID), repository.NewRedisLimiter(rdb, cfg.LoginLimit, cfg.LoginWindow), profiles, cfg.RefreshTTL)
	srv := grpcx.NewServer(log, tokens, map[string]struct{}{
		authv1.AuthService_Register_FullMethodName: {},
		authv1.AuthService_Login_FullMethodName:    {},
		authv1.AuthService_Refresh_FullMethodName:  {},
		authv1.AuthService_Logout_FullMethodName:   {},
	})
	authv1.RegisterAuthServiceServer(srv, grpcserver.New(svc))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatal("listen", zap.Error(err))
	}
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("auth listening on " + cfg.GRPCAddr)
	if err := srv.Serve(lis); err != nil {
		log.Fatal("serve", zap.Error(err))
	}
}
