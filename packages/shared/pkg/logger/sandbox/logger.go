package sbxlogger

import (
	"context"

	"go.opentelemetry.io/otel/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type SandboxLoggerConfig struct {
	// ServiceName is the name of the service that the logger is being created for.
	// The service name is added to every log entry.
	ServiceName string
	// IsInternal differentiates between our (internal) logs, and user accessible (external) logs.
	// For external logger, we also disable stacktraces
	IsInternal       bool
	CollectorAddress string
}

func NewLogger(ctx context.Context, loggerProvider log.LoggerProvider, config SandboxLoggerConfig) *zap.Logger {
	level := zap.NewAtomicLevelAt(zap.DebugLevel)

	enableConsole := false
	var core zapcore.Core
	if !config.IsInternal && config.CollectorAddress != "" {
		// Add Vector exporter to the core
		vectorEncoder := zapcore.NewJSONEncoder(GetSandboxEncoderConfig())
		httpWriter := logger.NewHTTPWriter(ctx, config.CollectorAddress)
		core = zapcore.NewCore(
			vectorEncoder,
			httpWriter,
			level,
		)
	} else {
		core = logger.GetOTELCore(loggerProvider, config.ServiceName)
		enableConsole = true
	}

	lg, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:       config.ServiceName,
		IsInternal:        config.IsInternal,
		IsDebug:           true,
		DisableStacktrace: !config.IsInternal,
		InitialFields: []zap.Field{
			zap.String("logger", config.ServiceName),
		},
		Cores:         []zapcore.Core{core},
		EnableConsole: enableConsole,
	})
	if err != nil {
		panic(err)
	}

	return lg
}

func GetSandboxEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:       "timestamp",
		MessageKey:    "message",
		LevelKey:      "level",
		EncodeLevel:   zapcore.LowercaseLevelEncoder,
		NameKey:       "logger",
		StacktraceKey: "stacktrace",
		EncodeTime:    zapcore.RFC3339NanoTimeEncoder,
		LineEnding:    zapcore.DefaultLineEnding,
	}
}
