package sbxlogger

import (
	"context"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	sandboxLoggerInternal LoggerBuilder
	sandboxLoggerExternal LoggerBuilder
)

type LoggerBuilder interface {
	WithMetadata(m LoggerMetadata) Logger
}

type Logger interface {
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)

	Sync() error

	Metrics(metrics SandboxMetricsFields)
	Healthcheck(ok bool, alwaysReport bool)
}

type LoggerMetadata interface {
	LoggerMetadata() SandboxMetadata
}

type SandboxLoggerConfig struct {
	ServiceName      string
	IsInternal       bool
	IsDevelopment    bool
	CollectorAddress string
}

type SandboxMetadata struct {
	SandboxID  string
	TemplateID string
	TeamID     string
}

func (sm SandboxMetadata) LoggerMetadata() SandboxMetadata {
	return sm
}

func (sm SandboxMetadata) Fields() []zap.Field {
	return []zap.Field{
		zap.String("sandboxID", sm.SandboxID),
		zap.String("templateID", sm.TemplateID),
		zap.String("teamID", sm.TeamID),

		// Fields for Vector
		zap.String("instanceID", sm.SandboxID),
		zap.String("envID", sm.TemplateID),
	}
}

func SetSandboxLoggerInternal(ctx context.Context, config SandboxLoggerConfig) {
	sandboxLoggerInternal = newSandboxLogger(ctx, config)
}

func SetSandboxLoggerExternal(ctx context.Context, config SandboxLoggerConfig) {
	sandboxLoggerExternal = newSandboxLogger(ctx, config)
}

func I(m LoggerMetadata) Logger {
	return sandboxLoggerInternal.WithMetadata(m)
}

func E(m LoggerMetadata) Logger {
	return sandboxLoggerExternal.WithMetadata(m)
}

type sandboxLogger struct {
	logger                *zap.Logger
	healthCheckWasFailing atomic.Bool
}

type LoggerBuilderBase struct {
	logger *zap.Logger
}

func (sl *LoggerBuilderBase) WithMetadata(m LoggerMetadata) Logger {
	return &sandboxLogger{
		logger:                sl.logger.With(m.LoggerMetadata().Fields()...),
		healthCheckWasFailing: atomic.Bool{},
	}
}

func newSandboxLogger(ctx context.Context, config SandboxLoggerConfig) LoggerBuilder {
	level := zap.NewAtomicLevelAt(zap.DebugLevel)

	vectorCore := zapcore.NewNopCore()
	if !config.IsInternal && config.CollectorAddress != "" {
		// Add Vector exporter to the core
		vectorEncoder := zapcore.NewJSONEncoder(logger.GetEncoderConfig(zapcore.DefaultLineEnding))
		httpWriter := logger.NewBufferedHTTPWriter(ctx, config.CollectorAddress)
		vectorCore = zapcore.NewCore(
			vectorEncoder,
			httpWriter,
			level,
		)
	}

	lg, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   config.ServiceName,
		IsInternal:    config.IsInternal,
		IsDevelopment: config.IsDevelopment,
		IsDebug:       true,
		InitialFields: []zap.Field{
			zap.String("logger", config.ServiceName),
		},
		Cores: []zapcore.Core{vectorCore},
	})
	if err != nil {
		panic(err)
	}

	return &LoggerBuilderBase{
		logger: lg,
	}
}

func (sl *sandboxLogger) Debug(msg string, fields ...zap.Field) {
	sl.logger.Debug(msg, fields...)
}

func (sl *sandboxLogger) Info(msg string, fields ...zap.Field) {
	sl.logger.Info(msg, fields...)
}

func (sl *sandboxLogger) Warn(msg string, fields ...zap.Field) {
	sl.logger.Warn(msg, fields...)
}

func (sl *sandboxLogger) Error(msg string, fields ...zap.Field) {
	sl.logger.Error(msg, fields...)
}

func (sl *sandboxLogger) Sync() error {
	return sl.logger.Sync()
}

type SandboxMetricsFields struct {
	Timestamp      int64
	CPUCount       uint32
	CPUUsedPercent float32
	MemTotalMiB    uint64
	MemUsedMiB     uint64
}

func (sl *sandboxLogger) Metrics(metrics SandboxMetricsFields) {
	sl.logger.Info(
		"",
		zap.String("category", "metrics"),
		zap.Float32("cpuUsedPct", metrics.CPUUsedPercent),
		zap.Uint32("cpuCount", metrics.CPUCount),
		zap.Uint64("memTotalMiB", metrics.MemTotalMiB),
		zap.Uint64("memUsedMiB", metrics.MemUsedMiB),
	)

	return
}

func (sl *sandboxLogger) Healthcheck(ok bool, alwaysReport bool) {
	if !ok && !sl.healthCheckWasFailing.Load() {
		sl.healthCheckWasFailing.Store(true)

		sl.logger.Error("Sandbox healthcheck started failing",
			zap.Bool("healthcheck", ok))

		return
	}
	if ok && sl.healthCheckWasFailing.Load() {
		sl.healthCheckWasFailing.Store(false)

		sl.logger.Info("Sandbox healthcheck recovered",
			zap.Bool("healthcheck", ok))

		return
	}

	if alwaysReport {
		if ok {
			sl.logger.Info(
				"Control sandbox healthcheck was successful",
				zap.Bool("healthcheck", ok))
		} else {
			sl.logger.Error("Control sandbox healthcheck was unsuccessful",
				zap.Bool("healthcheck", ok))
		}
	}
}
