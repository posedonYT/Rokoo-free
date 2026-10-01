package main

import (
	"context"
	"flag"
	"net"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	guildv1 "rokko.dev/gen/guild/v1"
	messagev1 "rokko.dev/gen/message/v1"
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
	"rokko.dev/services/message/internal/clients"
	msgcfg "rokko.dev/services/message/internal/config"
	"rokko.dev/services/message/internal/events"
	"rokko.dev/services/message/internal/repository"
	"rokko.dev/services/message/internal/service"
	grpcserver "rokko.dev/services/message/internal/transport/grpc"
	"rokko.dev/services/message/migrations"
)

func main() {
	path := flag.String("config", "config.yaml", "config file")
	flag.Parse()
	var cfg msgcfg.Config
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
	pool, err := postgres.NewPool(ctx, postgres.Config{DSN: cfg.PostgresDSN, ApplicationName: "message"})
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pool.Close()
	tokens, err := authn.Load("", cfg.PublicKeyPath, cfg.Issuer, 0)
	if err != nil {
		log.Fatal("keys", zap.Error(err))
	}
	guildConn, err := grpcx.Dial(cfg.GuildServiceAddr)
	if err != nil {
		log.Fatal("guild dial", zap.Error(err))
	}
	defer guildConn.Close()
	userConn, err := grpcx.Dial(cfg.UserServiceAddr)
	if err != nil {
		log.Fatal("user dial", zap.Error(err))
	}
	defer userConn.Close()
	writer := kafkax.NewWriter(cfg.KafkaBrokers)
	defer writer.Close()
	go outbox.RunRelayLogged(ctx, pool, writer, func(err error) { log.Warn("outbox", zap.Error(err)) })
	store := repository.NewPostgres(pool)
	go func() {
		if err := kafkax.Consume(ctx, cfg.KafkaBrokers, "message-service", []string{"guild.events"}, events.Handle(store)); err != nil && ctx.Err() == nil {
			log.Warn("guild events consumer stopped", zap.Error(err))
		}
	}()
	svc := service.New(store, store, clients.NewGuild(guildv1.NewGuildServiceClient(guildConn)), clients.NewBlocks(userv1.NewUserServiceClient(userConn)), id.New(cfg.WorkerID))
	srv := grpcx.NewServer(log, tokens, nil)
	messagev1.RegisterMessageServiceServer(srv, grpcserver.New(svc))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatal("listen", zap.Error(err))
	}
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("message listening on " + cfg.GRPCAddr)
	if err := srv.Serve(lis); err != nil {
		log.Fatal("serve", zap.Error(err))
	}
}
