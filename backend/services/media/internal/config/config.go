package config

type Config struct {
	LogLevel      string   `mapstructure:"log_level"`
	GRPCAddr      string   `mapstructure:"grpc_addr"`
	PostgresDSN   string   `mapstructure:"postgres_dsn"`
	PublicKeyPath string   `mapstructure:"public_key_path"`
	Issuer        string   `mapstructure:"issuer"`
	WorkerID      int64    `mapstructure:"worker_id"`
	MinioEndpoint string   `mapstructure:"minio_endpoint"`
	MinioAccess   string   `mapstructure:"minio_access_key"`
	MinioSecret   string   `mapstructure:"minio_secret_key"`
	MinioBucket   string   `mapstructure:"minio_bucket"`
	MinioSecure   bool     `mapstructure:"minio_secure"`
	KafkaBrokers  []string `mapstructure:"kafka_brokers"`
}
