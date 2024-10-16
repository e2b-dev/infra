package logs

import (
	"context"
	"io"
	"math"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs/exporter"
)

const (
	OrchestratorServiceName = "orchestrator"
	cpuUsageThreshold       = 0.85
	memoryUsageThreshold    = 0.85
)

type SandboxLogExporter struct {
	logger *zerolog.Logger
}

func NewSandboxLogExporter(ctx context.Context, debug bool, serviceName, address string) *SandboxLogExporter {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	exporters := []io.Writer{exporter.NewHTTPLogsExporter(ctx, debug, address)}

	l := zerolog.
		New(io.MultiWriter(exporters...)).
		With().
		Timestamp().
		Logger().
		Level(zerolog.DebugLevel).
		With().Str("logger", serviceName).Logger()

	return &SandboxLogExporter{
		logger: &l,
	}
}

type SandboxLogger struct {
	exporter              *SandboxLogExporter
	instanceID            string
	envID                 string
	teamID                string
	cpuMax                int32
	cpuWasAboveTreshold   atomic.Bool
	memoryMax             int32
	memoryWasAbove        atomic.Int32
	healthCheckWasFailing atomic.Bool
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
	// Round to 3 decimal places and cap at cpuMax
	cpu = math.Min(float64(int(cpu*1000))/1000, float64(l.cpuMax))
	if cpu > cpuUsageThreshold*float64(l.cpuMax) {
		l.cpuWasAboveTreshold.Store(true)

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpu", cpu).
			Int32("cpuMax", l.cpuMax).
			Msgf("cpu usage reached %d %% of total cpu", int(cpu/float64(l.cpuMax)*100))
	} else if l.cpuWasAboveTreshold.Load() && cpu <= cpuUsageThreshold*float64(l.cpuMax) {
		l.cpuWasAboveTreshold.Store(false)
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpu", cpu).
			Int32("cpuMax", l.cpuMax).
			Msgf("cpu usage fell below %d %% of total cpu", int(cpuUsageThreshold*100))
	}
}

func (l *SandboxLogger) MemoryUsage(memory float64) {
	// Cap at memoryMax
	memory = math.Min(memory, float64(l.memoryMax))
	if memory > memoryUsageThreshold*float64(l.memoryMax) && int32(memory) > l.memoryWasAbove.Load() {
		l.memoryWasAbove.Store(int32(memory))

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("memory", memory).
			Int32("memoryMax", l.memoryMax).
			Msgf("memory usage reached %d %% of memory", int(memory/float64(l.memoryMax)*100))
		return
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
