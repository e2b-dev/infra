package testutils

import "go.uber.org/zap"

func NewLogger() *zap.Logger {
	cfg := zap.NewDevelopmentConfig()

	logger, err := cfg.Build()

	if err != nil {
		panic(err)
	}

	return logger
}
