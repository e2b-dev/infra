package logger

import (
	"context"
	"fmt"
	"os"
	"time"

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
				"service":  loggerConfig.ServiceName,
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

	cores := make([]zapcore.Core, 0)

	if loggerConfig.CollectorAddress != "" {
		// Add Vector exporter to the core
		vectorEncoder := zapcore.NewJSONEncoder(config.EncoderConfig)
		httpWriter := &zapcore.BufferedWriteSyncer{
			WS:            NewHTTPWriter(ctx, loggerConfig.CollectorAddress),
			Size:          256 * 1024, // 256 kB
			FlushInterval: 5 * time.Second,
		}
		go func() {
			select {
			case <-ctx.Done():
				if err := httpWriter.Stop(); err != nil {
					fmt.Printf("Error stopping HTTP writer: %v\n", err)
				}
			}
		}()

		cores = append(cores, zapcore.NewCore(
			vectorEncoder,
			httpWriter,
			config.Level,
		))
	}

	logger = logger.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return zapcore.NewTee(cores...)
	}))

	return logger, nil
}
