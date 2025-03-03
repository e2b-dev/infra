package sbxlogger

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type SandboxLoggerConfig struct {
	ServiceName      string
	IsInternal       bool
	IsDevelopment    bool
	CollectorAddress string
}

func NewLogger(ctx context.Context, config SandboxLoggerConfig) *zap.Logger {
	level := zap.NewAtomicLevelAt(zap.DebugLevel)

	core := zapcore.NewNopCore()
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
		ServiceName:   config.ServiceName,
		IsInternal:    config.IsInternal,
		IsDevelopment: config.IsDevelopment,
		IsDebug:       true,
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
