package logger

import "go.uber.org/zap"

func New(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	if level == "debug" {
		cfg = zap.NewDevelopmentConfig()
	}
	if level != "" && level != "debug" {
		if err := cfg.Level.UnmarshalText([]byte(level)); err != nil {
			return nil, err
		}
	}
	return cfg.Build()
}
