package sbxlogger

import "go.uber.org/zap"

type SandboxLogger struct {
	*zap.Logger
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

func (sl *SandboxLogger) Metrics(metrics SandboxMetricsFields) {
	sl.Info(
		"",
		zap.String("category", "metrics"),
		zap.Float32("cpuUsedPct", metrics.CPUUsedPercent),
		zap.Uint32("cpuCount", metrics.CPUCount),
		zap.Uint64("memTotalMiB", metrics.MemTotalMiB),
		zap.Uint64("memUsedMiB", metrics.MemUsedMiB),
	)
}

func (sl *SandboxLogger) Healthcheck(action HealthCheckAction) {
	switch {
	case action == Success:
		sl.Info("Sandbox healthcheck recovered",
			zap.Bool("healthcheck", true))
	case action == Fail:
		sl.Error("Sandbox healthcheck started failing",
			zap.Bool("healthcheck", false))
	case action == ReportSuccess:
		sl.Info(
			"Control sandbox healthcheck was successful",
			zap.Bool("healthcheck", true))
	case action == ReportFail:
		sl.Error("Control sandbox healthcheck was unsuccessful",
			zap.Bool("healthcheck", false))
	}
}
