package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging/exporter"
)

func NewCollectorLogger() (*zap.SugaredLogger, error) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:       "timestamp",
		MessageKey:    "message",
		LevelKey:      "level",
		EncodeLevel:   zapcore.LowercaseLevelEncoder,
		NameKey:       "logger",
		StacktraceKey: "stacktrace",
		EncodeTime:    zapcore.RFC3339TimeEncoder,
	}

	level := zap.NewAtomicLevelAt(zap.InfoLevel)

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(exporter.NewHTTPLogsExporter(env.IsLocal())),
		level,
	)

	logger := zap.New(core)

	return logger.Sugar(), nil
}
