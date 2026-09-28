package config

type Config struct {
	LogLevel           string   `mapstructure:"log_level"`
	HTTPAddr           string   `mapstructure:"http_addr"`
	KafkaBrokers       []string `mapstructure:"kafka_brokers"`
	PublicKeyPath      string   `mapstructure:"public_key_path"`
	Issuer             string   `mapstructure:"issuer"`
	GuildServiceAddr   string   `mapstructure:"guild_service_addr"`
	MessageServiceAddr string   `mapstructure:"message_service_addr"`
	STUN               string   `mapstructure:"stun_url"`
	TURNURL            string   `mapstructure:"turn_url"`
	TURNUsername       string   `mapstructure:"turn_username"`
	TURNPassword       string   `mapstructure:"turn_password"`
	TURNSecret         string   `mapstructure:"turn_secret"`
	TURNTTlSec         int      `mapstructure:"turn_ttl_sec"`
	RedisAddr          string   `mapstructure:"redis_addr"`
	RedisPassword      string   `mapstructure:"redis_password"`
	NodeID             string   `mapstructure:"node_id"`
	PublicWSURL        string   `mapstructure:"public_ws_url"`
	ICEUDPPort         int      `mapstructure:"ice_udp_port"`
	ICEHostIP          string   `mapstructure:"ice_host_ip"`
	MaxParticipants    int      `mapstructure:"max_participants"`
	MaxVideoPublishers int      `mapstructure:"max_video_publishers"`
}
