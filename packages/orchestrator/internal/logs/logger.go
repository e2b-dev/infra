package logs

import (
	"context"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/logs/exporter"

	"github.com/rs/zerolog"
)

const (
	cpuUsageThreshold    = 0.85
	memoryUsageThreshold = 0.85
)

type SandboxLogExporter struct {
	logger *zerolog.Logger
}

func NewSandboxLogExporter(ctx context.Context, debug bool, address string) *SandboxLogExporter {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	exporters := []io.Writer{}

	if debug {
		exporters = append(exporters, os.Stdout)
	} else {
		exporters = append(exporters, exporter.NewHTTPLogsExporter(ctx, false, address), os.Stdout)
	}

	l := zerolog.
		New(io.MultiWriter(exporters...)).
		With().
		Timestamp().
		Logger().
		Level(zerolog.DebugLevel)

	return &SandboxLogExporter{
		logger: &l,
	}
}

type SandboxLogger struct {
	exporter               *SandboxLogExporter
	instanceID             string
	envID                  string
	teamID                 string
	cpuMax                 float64
	cpuWasAboveTreshold    atomic.Bool
	memoryMax              float64
	memoryWasAboveTreshold atomic.Bool
	healthCheckWasFailing  atomic.Bool
}

func (l *SandboxLogExporter) CreateSandboxLogger(
	instanceID string,
	envID string,
	teamID string,
	cpuMax float64,
	memoryMax float64,
) *SandboxLogger {
	return &SandboxLogger{
		exporter:   l,
		instanceID: instanceID,
		envID:      envID,
		teamID:     teamID,
		cpuMax:     cpuMax,
		memoryMax:  memoryMax,
	}
}

func (l *SandboxLogger) Eventf(
	format string,
	v ...interface{},
) {
	l.exporter.logger.Debug().
		Str("instanceID", l.instanceID).
		Str("envID", l.envID).
		Str("teamID", l.teamID).
		Msgf(format, v...)
}

func (l *SandboxLogger) CPUUsage(cpu float64) {
	if cpu > cpuUsageThreshold*l.cpuMax {
		l.cpuWasAboveTreshold.Store(true)

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpu", cpu).
			Float64("cpuMax", l.cpuMax).
			Msgf("cpu usage exceeded %f", cpuUsageThreshold*l.cpuMax)
	} else if l.cpuWasAboveTreshold.Load() && cpu <= cpuUsageThreshold*l.cpuMax {
		l.cpuWasAboveTreshold.Store(false)
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpu", cpu).
			Float64("cpuMax", l.cpuMax).
			Msgf("cpu usage fell below %f", cpuUsageThreshold*l.cpuMax)
	}
}

func (l *SandboxLogger) MemoryUsage(memory float64) {
	if memory > memoryUsageThreshold*l.memoryMax {
		l.memoryWasAboveTreshold.Store(true)

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("memory", memory).
			Float64("memoryMax", l.memoryMax).
			Msgf("memory usage exceeded %fMB", memoryUsageThreshold*l.memoryMax)
	} else if l.memoryWasAboveTreshold.Load() && memory <= memoryUsageThreshold*l.memoryMax {
		l.memoryWasAboveTreshold.Store(false)
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("memory", memory).
			Float64("memoryMax", l.memoryMax).
			Msgf("memory usage fell below %fMB", memoryUsageThreshold*l.memoryMax)
	}
}

func (l *SandboxLogger) Healthcheck(ok bool) {
	if !ok && !l.healthCheckWasFailing.Load() {
		l.healthCheckWasFailing.Store(true)

		l.exporter.logger.Error().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Bool("healthcheck", ok).
			Msg("sandbox healthcheck started failing")
	} else if ok && l.healthCheckWasFailing.Load() {
		l.healthCheckWasFailing.Store(false)

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Bool("healthcheck", ok).
			Msg("sandbox healthcheck recovered")
	}
}
