package config

import (
	"strings"

	"github.com/spf13/viper"
)

func Load(path string, dst any) error {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return err
	}
	v.SetEnvPrefix("ROKKO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	for _, key := range v.AllKeys() {
		_ = v.BindEnv(key)
	}
	return v.Unmarshal(dst)
}
