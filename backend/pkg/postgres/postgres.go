package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	DSN               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	StatementTimeout  string
	LockTimeout       string
	IdleInTxTimeout   string
	ApplicationName   string
}

func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	parsed, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 10
	}
	if cfg.MinConns <= 0 {
		cfg.MinConns = 1
	}
	if cfg.MaxConnLifetime <= 0 {
		cfg.MaxConnLifetime = time.Hour
	}
	if cfg.MaxConnIdleTime <= 0 {
		cfg.MaxConnIdleTime = 15 * time.Minute
	}
	if cfg.HealthCheckPeriod <= 0 {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	if cfg.StatementTimeout == "" {
		cfg.StatementTimeout = "5s"
	}
	if cfg.LockTimeout == "" {
		cfg.LockTimeout = "2s"
	}
	if cfg.IdleInTxTimeout == "" {
		cfg.IdleInTxTimeout = "15s"
	}
	if cfg.ApplicationName == "" {
		cfg.ApplicationName = "rokko"
	}
	parsed.MaxConns = cfg.MaxConns
	parsed.MinConns = cfg.MinConns
	parsed.MaxConnLifetime = cfg.MaxConnLifetime
	parsed.MaxConnIdleTime = cfg.MaxConnIdleTime
	parsed.HealthCheckPeriod = cfg.HealthCheckPeriod
	if parsed.ConnConfig.RuntimeParams == nil {
		parsed.ConnConfig.RuntimeParams = map[string]string{}
	}
	parsed.ConnConfig.RuntimeParams["statement_timeout"] = cfg.StatementTimeout
	parsed.ConnConfig.RuntimeParams["lock_timeout"] = cfg.LockTimeout
	parsed.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = cfg.IdleInTxTimeout
	parsed.ConnConfig.RuntimeParams["application_name"] = cfg.ApplicationName
	pool, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		return nil, err
	}
	ping, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(ping); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return pool, nil
}

type Stats struct {
	Acquired int32
	Idle     int32
	Total    int32
	Max      int32
}

func ReadStats(pool *pgxpool.Pool) Stats {
	st := pool.Stat()
	return Stats{Acquired: st.AcquiredConns(), Idle: st.IdleConns(), Total: st.TotalConns(), Max: st.MaxConns()}
}
