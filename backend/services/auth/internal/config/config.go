package config

import "time"

type Config struct {
	LogLevel        string        `mapstructure:"log_level"`
	GRPCAddr        string        `mapstructure:"grpc_addr"`
	PostgresDSN     string        `mapstructure:"postgres_dsn"`
	RedisAddr       string        `mapstructure:"redis_addr"`
	RedisPassword   string        `mapstructure:"redis_password"`
	KafkaBrokers    []string      `mapstructure:"kafka_brokers"`
	PrivateKeyPath  string        `mapstructure:"private_key_path"`
	PublicKeyPath   string        `mapstructure:"public_key_path"`
	AccessTTL       time.Duration `mapstructure:"access_ttl"`
	RefreshTTL      time.Duration `mapstructure:"refresh_ttl"`
	Issuer          string        `mapstructure:"issuer"`
	UserServiceAddr string        `mapstructure:"user_service_addr"`
	LoginLimit      int64         `mapstructure:"login_limit"`
	LoginWindow     time.Duration `mapstructure:"login_window"`
	WorkerID        int64         `mapstructure:"worker_id"`
}
