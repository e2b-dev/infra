package logging

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(isLocal bool) (*zap.SugaredLogger, error) {
	config := zap.Config{
		Level:             zap.NewAtomicLevelAt(zap.InfoLevel),
		Development:       isLocal,
		DisableStacktrace: !isLocal,
		Encoding:          "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:       "timestamp",
			MessageKey:    "message",
			LevelKey:      "level",
			EncodeLevel:   zapcore.LowercaseLevelEncoder,
			NameKey:       "logger",
			StacktraceKey: "stacktrace",
			EncodeTime:    zapcore.RFC3339TimeEncoder,
		},
		OutputPaths: []string{
			"stdout",
		},
		ErrorOutputPaths: []string{
			"stderr",
		},
	}

	logger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("error building logger: %w", err)
	}

	zap.ReplaceGlobals(logger)
	return logger.Sugar(), nil
}
