package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
	userv1 "rokko.dev/gen/user/v1"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/config"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/kafkax"
	"rokko.dev/pkg/logger"
	"rokko.dev/pkg/redisx"
	rtcfg "rokko.dev/services/realtime/internal/config"
	"rokko.dev/services/realtime/internal/hub"
	"rokko.dev/services/realtime/internal/state"
	httpserver "rokko.dev/services/realtime/internal/transport/http"
)

func main() {
	path := flag.String("config", "config.yaml", "config file")
	flag.Parse()
	var cfg rtcfg.Config
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
	tokens, err := authn.Load("", cfg.PublicKeyPath, cfg.Issuer, 0)
	if err != nil {
		log.Fatal("keys", zap.Error(err))
	}
	rdb := redisx.New(redisx.Config{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	if err := redisx.Ping(ctx, rdb); err != nil {
		log.Fatal("redis", zap.Error(err))
	}
	defer rdb.Close()
	userConn, err := grpcx.Dial(cfg.UserServiceAddr)
	if err != nil {
		log.Fatal("user dial", zap.Error(err))
	}
	defer userConn.Close()
	st := state.New(rdb)
	h := hub.New()
	srv := httpserver.New(tokens, h, st, userv1.NewUserServiceClient(userConn))
	writer := kafkax.NewWriter(cfg.KafkaBrokers)
	defer writer.Close()
	srv.SetEvents(func(ctx context.Context, topic, key string, payload []byte) error {
		return writer.WriteMessages(ctx, kafka.Message{Topic: topic, Key: []byte(key), Value: payload})
	})
	srv.SetLog(func(msg string, err error) { log.Warn(msg, zap.Error(err)) })
	go srv.ListenRedis(ctx)
	go func() {
		_ = kafkax.Consume(ctx, cfg.KafkaBrokers, "realtime", []string{"user.events", "guild.events", "message.events", "voice.events"}, func(ctx context.Context, msg kafka.Message) error {
			var env eventx.Envelope
			if err := json.Unmarshal(msg.Value, &env); err != nil {
				return nil
			}
			return srv.Deliver(ctx, env)
		})
	}()
	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	log.Info("realtime listening on " + cfg.HTTPAddr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("http", zap.Error(err))
	}
}
