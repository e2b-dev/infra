package logging

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger interface {
	Debug(args ...interface{})

	Info(args ...interface{})
	Infof(template string, args ...interface{})

	Warn(args ...interface{})
	Warnf(template string, args ...interface{})

	Error(args ...interface{})
	Errorf(template string, args ...interface{})
	Errorw(msg string, keysAndValues ...interface{})

	Panic(args ...interface{})

	Fatalf(template string, args ...interface{})

	// Desugar is ZapLogger specific returns the underlying zap.Logger
	Desugar() *zap.Logger
}

func New(isLocal bool) (Logger, error) {
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

	return logger.Sugar(), nil
}
