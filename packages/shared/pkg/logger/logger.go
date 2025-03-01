package logger

import (
	"context"
	"fmt"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/log/global"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
)

type LoggerConfig struct {
	ServiceName   string
	IsInternal    bool
	IsDevelopment bool
	IsDebug       bool
	InitialFields []zap.Field

	Cores []zapcore.Core
}

func NewLogger(ctx context.Context, loggerConfig LoggerConfig) (*zap.Logger, error) {
	var level zap.AtomicLevel
	if loggerConfig.IsDebug {
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	config := zap.Config{
		Level:             level,
		Development:       loggerConfig.IsDevelopment,
		DisableStacktrace: false,
		Sampling:          nil,
		Encoding:          "json",
		EncoderConfig:     GetEncoderConfig(zapcore.DefaultLineEnding),
		OutputPaths: []string{
			"stdout",
		},
		ErrorOutputPaths: []string{
			"stderr",
		},
	}

	cores := make([]zapcore.Core, 0)

	if loggerConfig.IsInternal {
		provider := global.GetLoggerProvider()
		cores = append(cores,
			otelzap.NewCore(loggerConfig.ServiceName, otelzap.WithLoggerProvider(provider)),
		)
	}

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

func GetEncoderConfig(lineEnding string) zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:       "timestamp",
		MessageKey:    "message",
		LevelKey:      "level",
		EncodeLevel:   zapcore.LowercaseLevelEncoder,
		NameKey:       "logger",
		StacktraceKey: "stacktrace",
		EncodeTime:    zapcore.RFC3339TimeEncoder,
		LineEnding:    lineEnding,
	}
}
