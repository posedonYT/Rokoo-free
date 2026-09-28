package config

type Config struct {
	LogLevel         string   `mapstructure:"log_level"`
	GRPCAddr         string   `mapstructure:"grpc_addr"`
	PostgresDSN      string   `mapstructure:"postgres_dsn"`
	KafkaBrokers     []string `mapstructure:"kafka_brokers"`
	PublicKeyPath    string   `mapstructure:"public_key_path"`
	Issuer           string   `mapstructure:"issuer"`
	WorkerID         int64    `mapstructure:"worker_id"`
	GuildServiceAddr string   `mapstructure:"guild_service_addr"`
	UserServiceAddr  string   `mapstructure:"user_service_addr"`
}
