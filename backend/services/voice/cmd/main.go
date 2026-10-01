package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
	guildv1 "rokko.dev/gen/guild/v1"
	messagev1 "rokko.dev/gen/message/v1"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/config"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/kafkax"
	"rokko.dev/pkg/logger"
	"rokko.dev/pkg/redisx"
	"rokko.dev/services/voice/internal/access"
	voicecfg "rokko.dev/services/voice/internal/config"
	"rokko.dev/services/voice/internal/registry"
	"rokko.dev/services/voice/internal/sfu"
	httpserver "rokko.dev/services/voice/internal/transport/http"
)

func main() {
	path := flag.String("config", "config.yaml", "config file")
	flag.Parse()
	var cfg voicecfg.Config
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
	guildConn, err := grpcx.Dial(cfg.GuildServiceAddr)
	if err != nil {
		log.Fatal("guild dial", zap.Error(err))
	}
	defer guildConn.Close()
	msgConn, err := grpcx.Dial(cfg.MessageServiceAddr)
	if err != nil {
		log.Fatal("message dial", zap.Error(err))
	}
	defer msgConn.Close()
	writer := kafkax.NewWriter(cfg.KafkaBrokers)
	defer writer.Close()
	var reg *registry.Registry
	var rdb *redis.Client
	if cfg.RedisAddr != "" {
		rdb = redisx.New(redisx.Config{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
		defer rdb.Close()
		if err := redisx.Ping(ctx, rdb); err != nil {
			log.Fatal("redis", zap.Error(err))
		}
		reg = registry.New(rdb, 15*time.Second)
	}
	ice := []webrtc.ICEServer{{URLs: []string{cfg.STUN}}}
	secret := cfg.TURNSecret
	if secret == "" {
		secret = cfg.TURNPassword
	}
	if cfg.TURNURL != "" && secret == "" {
		ice = append(ice, webrtc.ICEServer{URLs: []string{cfg.TURNURL}, Username: cfg.TURNUsername, Credential: cfg.TURNPassword})
	}
	nodeID := cfg.NodeID
	if nodeID == "" {
		nodeID = "voice"
	}
	publicURL := cfg.PublicWSURL
	if publicURL == "" {
		publicURL = "ws://localhost:8082/ws"
	}
	api, err := sfu.NewAPI(sfu.APIOptions{UDPPort: cfg.ICEUDPPort, HostIP: cfg.ICEHostIP})
	if err != nil {
		log.Fatal("webrtc api", zap.Error(err))
	}
	ttl := time.Duration(cfg.TURNTTlSec) * time.Second
	srv := httpserver.New(httpserver.Options{
		Tokens:             tokens,
		Access:             access.New(guildv1.NewGuildServiceClient(guildConn), messagev1.NewMessageServiceClient(msgConn)),
		Pub:                kafkaPublisher{w: writer},
		ICE:                ice,
		API:                api,
		Registry:           reg,
		NodeID:             nodeID,
		PublicURL:          publicURL,
		Log:                log,
		MaxParticipants:    cfg.MaxParticipants,
		MaxVideoPublishers: cfg.MaxVideoPublishers,
		STUN:               cfg.STUN,
		TURNURL:            cfg.TURNURL,
		TURNSecret:         secret,
		TURNTTl:            ttl,
		Ready: func(ctx context.Context) error {
			if rdb != nil {
				if err := rdb.Ping(ctx).Err(); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if reg != nil {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					srv.RefreshRooms(ctx)
				}
			}
		}()
		go func() {
			_ = kafkax.Consume(ctx, cfg.KafkaBrokers, "voice-"+nodeID, []string{"voice.events"}, func(ctx context.Context, msg kafka.Message) error {
				var env eventx.Envelope
				if json.Unmarshal(msg.Value, &env) != nil || env.Type != "voice.call.declined" {
					return nil
				}
				var payload map[string]string
				if json.Unmarshal(env.Payload, &payload) != nil {
					return nil
				}
				srv.HandleDeclined(ctx, payload["room_id"], payload["user_id"])
				return nil
			})
		}()
	}
	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Drain(drain)
		shutdown, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_ = httpSrv.Shutdown(shutdown)
	}()
	log.Info("voice listening", zap.String("addr", cfg.HTTPAddr), zap.String("node", nodeID))
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("http", zap.Error(err))
	}
}

type kafkaPublisher struct{ w *kafka.Writer }

func (p kafkaPublisher) Publish(ctx context.Context, topic, key string, payload []byte) error {
	return p.w.WriteMessages(ctx, kafka.Message{Topic: topic, Key: []byte(key), Value: payload})
}
