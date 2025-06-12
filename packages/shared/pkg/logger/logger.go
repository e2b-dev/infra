package logger

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type LoggerConfig struct {
	// ServiceName is the name of the service that the logger is being created for.
	// The service name is added to every log entry.
	ServiceName string
	// IsInternal differentiates between our (internal) logs, and user accessible (external) logs.
	IsInternal bool
	// IsDebug enables debug level logging, otherwise zap.InfoLevel level is used.
	IsDebug bool
	// DisableStacktrace disables stacktraces for the logger.
	DisableStacktrace bool

	// InitialFields fields that are added to every log entry.
	InitialFields []zap.Field
	// Cores additional processing cores for the logger.
	Cores []zapcore.Core
}

func NewLogger(_ context.Context, loggerConfig LoggerConfig) (*zap.Logger, error) {
	var level zap.AtomicLevel
	if loggerConfig.IsDebug {
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	config := zap.Config{
		DisableStacktrace: loggerConfig.DisableStacktrace,
		// Takes stacktraces more liberally
		Development: true,
		Sampling:    nil,

		// Console core
		Encoding:      "console",
		EncoderConfig: GetConsoleEncoderConfig(),
		Level:         level,
		OutputPaths: []string{
			"stdout",
		},
		ErrorOutputPaths: []string{
			"stderr",
		},
	}

	cores := make([]zapcore.Core, 0)
	cores = append(cores, loggerConfig.Cores...)

	logger, err := config.Build(
		zap.WrapCore(func(c zapcore.Core) zapcore.Core {
			cores = append(cores, c)

			return zapcore.NewTee(cores...)
		}),
		zap.Fields(
			zap.String("service", loggerConfig.ServiceName),
			zap.Bool("internal", loggerConfig.IsInternal),
			zap.Int("pid", os.Getpid()),
		),
		zap.Fields(loggerConfig.InitialFields...),
	)
	if err != nil {
		return nil, fmt.Errorf("error building logger: %w", err)
	}

	return logger, nil
}

func GetConsoleEncoderConfig() zapcore.EncoderConfig {
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.CallerKey = zapcore.OmitKey
	cfg.ConsoleSeparator = "  "

	return cfg
}

func GetOTELCore(provider log.LoggerProvider, serviceName string) zapcore.Core {
	return otelzap.NewCore(serviceName, otelzap.WithLoggerProvider(provider))
}
