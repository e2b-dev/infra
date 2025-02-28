package sbxlogger

import (
	"context"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type SandboxLogger struct {
	logger                *zap.Logger
	healthCheckWasFailing atomic.Bool
}

type SandboxLoggerConfig struct {
	ServiceName      string
	IsInternal       bool
	IsDevelopment    bool
	SandboxID        string
	TemplateID       string
	TeamID           string
	CollectorAddress string
}

func NewSandboxLogger(ctx context.Context, config SandboxLoggerConfig) *SandboxLogger {
	level := zap.NewAtomicLevelAt(zap.DebugLevel)

	vectorCore := zapcore.NewNopCore()
	if !config.IsInternal && config.CollectorAddress != "" {
		// Add Vector exporter to the core
		vectorEncoder := zapcore.NewJSONEncoder(logger.GetEncoderConfig())
		httpWriter := logger.NewHTTPWriter(ctx, config.CollectorAddress)
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

			zap.String("sandboxID", config.SandboxID),
			zap.String("templateID", config.TemplateID),
			zap.String("teamID", config.TeamID),

			// Fields for Vector
			zap.String("instanceID", config.SandboxID),
			zap.String("envID", config.TemplateID),
		},
		Cores: []zapcore.Core{vectorCore},
	})
	if err != nil {
		panic(err)
	}

	return &SandboxLogger{
		logger:                lg,
		healthCheckWasFailing: atomic.Bool{},
	}
}

func (sl *SandboxLogger) Debug(msg string, fields ...zap.Field) {
	sl.logger.Debug(msg, fields...)
}

func (sl *SandboxLogger) Info(msg string, fields ...zap.Field) {
	sl.logger.Info(msg, fields...)
}

func (sl *SandboxLogger) Warn(msg string, fields ...zap.Field) {
	sl.logger.Warn(msg, fields...)
}

func (sl *SandboxLogger) Error(msg string, fields ...zap.Field) {
	sl.logger.Error(msg, fields...)
}

type SandboxMetricsFields struct {
	Timestamp      int64
	CPUCount       uint32
	CPUUsedPercent float32
	MemTotalMiB    uint64
	MemUsedMiB     uint64
}

func (sl *SandboxLogger) Metrics(metrics SandboxMetricsFields) {
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

func (sl *SandboxLogger) Healthcheck(ok bool, alwaysReport bool) {
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
