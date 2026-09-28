package redisx

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Addr         string
	Password     string
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxRetries   int
	TLS          bool
}

func New(cfg Config) *redis.Client {
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 20
	}
	if cfg.MinIdleConns <= 0 {
		cfg.MinIdleConns = 2
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 3 * time.Second
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 2 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 2 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	opts := &redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		MaxRetries:   cfg.MaxRetries,
	}
	if cfg.TLS {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return redis.NewClient(opts)
}

func Ping(ctx context.Context, c *redis.Client) error {
	ping, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Ping(ping).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}

const allowScript = `
local n = redis.call('INCR', KEYS[1])
if redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`

const slidingScript = `
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
local n = redis.call('ZCARD', KEYS[1])
if n >= limit then
  return 0
end
redis.call('ZADD', KEYS[1], now, ARGV[4])
redis.call('PEXPIRE', KEYS[1], window)
return 1
`

func Allow(ctx context.Context, c *redis.Client, key string, limit int64, window time.Duration) (bool, error) {
	n, err := c.Eval(ctx, allowScript, []string{key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return n <= limit, nil
}

func AllowSliding(ctx context.Context, c *redis.Client, key string, limit int64, window time.Duration, now time.Time, member string) (bool, error) {
	n, err := c.Eval(ctx, slidingScript, []string{key}, now.UnixMilli(), window.Milliseconds(), limit, member).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func ParseInt(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
