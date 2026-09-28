package main

import (
	"context"
	"flag"
	"net"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	mediav1 "rokko.dev/gen/media/v1"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/config"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/kafkax"
	"rokko.dev/pkg/logger"
	"rokko.dev/pkg/migratex"
	"rokko.dev/pkg/outbox"
	"rokko.dev/pkg/postgres"
	mediacfg "rokko.dev/services/media/internal/config"
	"rokko.dev/services/media/internal/repository"
	"rokko.dev/services/media/internal/service"
	"rokko.dev/services/media/internal/storage"
	grpcserver "rokko.dev/services/media/internal/transport/grpc"
	"rokko.dev/services/media/migrations"
)

func main() {
	path := flag.String("config", "config.yaml", "config file")
	flag.Parse()
	var cfg mediacfg.Config
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
	pool, err := postgres.NewPool(ctx, postgres.Config{DSN: cfg.PostgresDSN, ApplicationName: "media"})
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pool.Close()
	if len(cfg.KafkaBrokers) > 0 {
		writer := kafkax.NewWriter(cfg.KafkaBrokers)
		defer writer.Close()
		go outbox.RunRelayLogged(ctx, pool, writer, func(err error) { log.Warn("outbox", zap.Error(err)) })
	}
	tokens, err := authn.Load("", cfg.PublicKeyPath, cfg.Issuer, 0)
	if err != nil {
		log.Fatal("keys", zap.Error(err))
	}
	store, err := storage.New(cfg.MinioEndpoint, cfg.MinioAccess, cfg.MinioSecret, cfg.MinioSecure)
	if err != nil {
		log.Fatal("minio", zap.Error(err))
	}
	if err := store.EnsureBucket(ctx, cfg.MinioBucket); err != nil {
		log.Fatal("bucket", zap.Error(err))
	}
	svc := service.New(repository.NewPostgres(pool), store, id.New(cfg.WorkerID), cfg.MinioBucket)
	srv := grpcx.NewServer(log, tokens, nil)
	mediav1.RegisterMediaServiceServer(srv, grpcserver.New(svc))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatal("listen", zap.Error(err))
	}
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("media listening on " + cfg.GRPCAddr)
	if err := srv.Serve(lis); err != nil {
		log.Fatal("serve", zap.Error(err))
	}
}
