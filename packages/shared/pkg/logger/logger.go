package logger

import (
	"context"
	"fmt"
	"io"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type LoggerConfig struct {
	ServiceName   string
	IsInternal    bool
	IsDevelopment bool
	IsDebug       bool
	InitialFields map[string]interface{}

	CollectorAddress string
}

func NewLogger(ctx context.Context, loggerConfig LoggerConfig) (*zap.Logger, error) {
	var level zap.AtomicLevel
	if loggerConfig.IsDebug {
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	exporters := []io.Writer{}
	if loggerConfig.CollectorAddress != "" {
		exporter := NewHTTPLogsExporter(
			ctx, loggerConfig.CollectorAddress, loggerConfig.IsDevelopment)
		exporters = append(exporters, exporter)
	}

	config := zap.Config{
		Level:             level,
		Development:       loggerConfig.IsDevelopment,
		DisableStacktrace: false,
		Sampling:          nil,
		Encoding:          "json",
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
		InitialFields: func() map[string]interface{} {
			fields := map[string]interface{}{
				"internal": loggerConfig.IsInternal,
				"pid":      os.Getpid(),
			}
			for k, v := range loggerConfig.InitialFields {
				fields[k] = v
			}
			return fields
		}(),
	}

	logger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("error building logger: %w", err)
	}

	if len(exporters) > 0 {
		core := zapcore.NewCore(
			zapcore.NewJSONEncoder(config.EncoderConfig),
			zapcore.AddSync(io.MultiWriter(exporters...)),
			config.Level,
		)
		logger = logger.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
			return zapcore.NewTee(c, core)
		}))
	}

	return logger, nil
}
