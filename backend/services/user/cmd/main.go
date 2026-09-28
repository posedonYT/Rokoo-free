package main

import (
	"context"
	"flag"
	"net"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
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
	usercfg "rokko.dev/services/user/internal/config"
	"rokko.dev/services/user/internal/events"
	"rokko.dev/services/user/internal/repository"
	"rokko.dev/services/user/internal/service"
	grpcserver "rokko.dev/services/user/internal/transport/grpc"
	"rokko.dev/services/user/migrations"
)

func main() {
	path := flag.String("config", "config.yaml", "config file")
	flag.Parse()
	var cfg usercfg.Config
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
	pool, err := postgres.NewPool(ctx, postgres.Config{DSN: cfg.PostgresDSN, ApplicationName: "user"})
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pool.Close()
	tokens, err := authn.Load("", cfg.PublicKeyPath, cfg.Issuer, 0)
	if err != nil {
		log.Fatal("keys", zap.Error(err))
	}
	writer := kafkax.NewWriter(cfg.KafkaBrokers)
	defer writer.Close()
	go outbox.RunRelayLogged(ctx, pool, writer, func(err error) { log.Warn("outbox", zap.Error(err)) })
	svc := service.New(repository.NewPostgres(pool), id.New(cfg.WorkerID))
	go func() {
		_ = kafkax.Consume(ctx, cfg.KafkaBrokers, "user-service", []string{"user.events"}, events.Handle(svc))
	}()
	srv := grpcx.NewServer(log, tokens, nil)
	userv1.RegisterUserServiceServer(srv, grpcserver.New(svc))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatal("listen", zap.Error(err))
	}
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("user listening on " + cfg.GRPCAddr)
	if err := srv.Serve(lis); err != nil {
		log.Fatal("serve", zap.Error(err))
	}
}
