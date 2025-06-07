package sbxlogger

import (
	"context"

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

func NewLogger(ctx context.Context, config SandboxLoggerConfig) *zap.Logger {
	level := zap.NewAtomicLevelAt(zap.DebugLevel)

	var core zapcore.Core
	if !config.IsInternal && config.CollectorAddress != "" {
		// Add Vector exporter to the core
		vectorEncoder := zapcore.NewJSONEncoder(logger.GetEncoderConfig(zapcore.DefaultLineEnding))
		httpWriter := logger.NewBufferedHTTPWriter(ctx, config.CollectorAddress)
		core = zapcore.NewCore(
			vectorEncoder,
			httpWriter,
			level,
		)
	} else {
		core = logger.GetOTELCore(config.ServiceName)
	}

	lg, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:       config.ServiceName,
		IsInternal:        config.IsInternal,
		IsDebug:           true,
		DisableStacktrace: !config.IsInternal,
		InitialFields: []zap.Field{
			zap.String("logger", config.ServiceName),
		},
		Cores: []zapcore.Core{core},
	})
	if err != nil {
		panic(err)
	}

	return lg
}
