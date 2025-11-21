package sbxlogger

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type SandboxLogger struct {
	logger.Logger
}

type HealthCheckAction int

const (
	Success HealthCheckAction = iota
	Fail
	ReportSuccess
	ReportFail
)

type SandboxMetricsFields struct {
	Timestamp      int64
	CPUCount       uint32
	CPUUsedPercent float32
	MemTotalMiB    uint64
	MemUsedMiB     uint64
}

func (sl *SandboxLogger) Metrics(ctx context.Context, metrics SandboxMetricsFields) {
	sl.Info(
		ctx,
		"",
		zap.String("category", "metrics"),
		zap.Float32("cpuUsedPct", metrics.CPUUsedPercent),
		zap.Uint32("cpuCount", metrics.CPUCount),
		zap.Uint64("memTotalMiB", metrics.MemTotalMiB),
		zap.Uint64("memUsedMiB", metrics.MemUsedMiB),
	)
}

func (sl *SandboxLogger) Healthcheck(ctx context.Context, action HealthCheckAction) {
	switch action {
	case Success:
		sl.Info(ctx, "Sandbox healthcheck recovered",
			zap.Bool("healthcheck", true))
	case Fail:
		sl.Error(ctx, "Sandbox healthcheck started failing",
			zap.Bool("healthcheck", false))
	case ReportSuccess:
		sl.Info(
			ctx,
			"Control sandbox healthcheck was successful",
			zap.Bool("healthcheck", true))
	case ReportFail:
		sl.Error(ctx, "Control sandbox healthcheck was unsuccessful",
			zap.Bool("healthcheck", false))
	}
}
