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
	cpuMax                 int32
	cpuWasAboveTreshold    atomic.Bool
	memoryMax              int32
	memoryWasAboveTreshold atomic.Bool
	healthCheckWasFailing  atomic.Bool
}

func (l *SandboxLogExporter) CreateSandboxLogger(
	instanceID string,
	envID string,
	teamID string,
	cpuMax int32,
	memoryMax int32,
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
	if cpu > cpuUsageThreshold*float64(l.cpuMax) {
		l.cpuWasAboveTreshold.Store(true)

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpu", cpu).
			Int32("cpuMax", l.cpuMax).
			Msgf("cpu usage exceeded %f", cpuUsageThreshold*float64(l.cpuMax))
	} else if l.cpuWasAboveTreshold.Load() && cpu <= cpuUsageThreshold*float64(l.cpuMax) {
		l.cpuWasAboveTreshold.Store(false)
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpu", cpu).
			Int32("cpuMax", l.cpuMax).
			Msgf("cpu usage fell below %f", cpuUsageThreshold*float64(l.cpuMax))
	}
}

func (l *SandboxLogger) MemoryUsage(memory float64) {
	if memory > memoryUsageThreshold*float64(l.memoryMax) {
		l.memoryWasAboveTreshold.Store(true)

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("memory", memory).
			Int32("memoryMax", l.memoryMax).
			Msgf("memory usage exceeded %fMB", memoryUsageThreshold*float64(l.memoryMax))
	} else if l.memoryWasAboveTreshold.Load() && memory <= memoryUsageThreshold*float64(l.memoryMax) {
		l.memoryWasAboveTreshold.Store(false)
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("memory", memory).
			Int32("memoryMax", l.memoryMax).
			Msgf("memory usage fell below %fMB", memoryUsageThreshold*float64(l.memoryMax))
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
