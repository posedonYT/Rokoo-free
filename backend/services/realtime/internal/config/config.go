package config

type Config struct {
	LogLevel        string   `mapstructure:"log_level"`
	HTTPAddr        string   `mapstructure:"http_addr"`
	RedisAddr       string   `mapstructure:"redis_addr"`
	RedisPassword   string   `mapstructure:"redis_password"`
	KafkaBrokers    []string `mapstructure:"kafka_brokers"`
	PublicKeyPath   string   `mapstructure:"public_key_path"`
	Issuer          string   `mapstructure:"issuer"`
	UserServiceAddr string   `mapstructure:"user_service_addr"`
}
