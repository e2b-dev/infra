package sbxlogger

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"go.uber.org/zap"
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
	logger, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   config.ServiceName,
		IsInternal:    config.IsInternal,
		IsDevelopment: config.IsDevelopment,
		IsDebug:       true,
		InitialFields: map[string]interface{}{
			"sandboxID":  config.SandboxID,
			"templateID": config.TemplateID,
			"teamID":     config.TeamID,
		},
		CollectorAddress: config.CollectorAddress,
	})
	if err != nil {
		panic(err)
	}

	return &SandboxLogger{
		logger: logger,
	}
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

		sl.logger.Error("",
			zap.Error(fmt.Errorf("Sandbox healthcheck started failing")),
			zap.Bool("healthcheck", ok))

		return
	}
	if ok && sl.healthCheckWasFailing.Load() {
		sl.healthCheckWasFailing.Store(false)

		sl.logger.Warn("",
			zap.Error(fmt.Errorf("Sandbox healthcheck recovered")),
			zap.Bool("healthcheck", ok))

		return
	}

	if alwaysReport {
		if ok {
			sl.logger.Info(
				"Control sandbox healthcheck was successful",
				zap.Bool("healthcheck", ok))
		} else {
			sl.logger.Error("",
				zap.Error(fmt.Errorf("Sandbox healthcheck failed")),
				zap.Bool("healthcheck", ok))
		}
	}
}
