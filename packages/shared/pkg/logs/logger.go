package logs

import (
	"context"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/exporter"
)

const (
	OrchestratorServiceName = "orchestrator"
	cpuUsageThreshold       = 0.85
	memoryUsageThreshold    = 0.85
)

type sandboxLogExporter struct {
	logger *zerolog.Logger
}

func newSandboxLogExporter(serviceName string) *sandboxLogExporter {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	ctx := context.Background()
	exporters := []io.Writer{exporter.NewHTTPLogsExporter(ctx, consts.LogsProxyAddress)}

	l := zerolog.
		New(io.MultiWriter(exporters...)).
		With().
		Timestamp().
		Logger().
		Level(zerolog.DebugLevel).
		With().Str("logger", serviceName).Logger()

	return &sandboxLogExporter{
		logger: &l,
	}
}

var (
	logsExporter   *sandboxLogExporter
	logsExporterMU = sync.Mutex{}
)

func getSandboxLogExporter() *sandboxLogExporter {
	logsExporterMU.Lock()
	defer logsExporterMU.Unlock()

	if logsExporter == nil {
		logsExporter = newSandboxLogExporter(OrchestratorServiceName)
	}

	return logsExporter
}

type SandboxLogger struct {
	exporter              *sandboxLogExporter
	internal              bool
	instanceID            string
	envID                 string
	teamID                string
	cpuMax                int32
	cpuWasAboveTreshold   atomic.Bool
	memoryMiBMax          int32
	memoryWasAbove        atomic.Int32
	healthCheckWasFailing atomic.Bool
}

func NewSandboxLogger(
	instanceID string,
	envID string,
	teamID string,
	cpuMax int32,
	memoryMax int32,
	internal bool,
) *SandboxLogger {
	sbxLogExporter := getSandboxLogExporter()
	return &SandboxLogger{
		exporter:     sbxLogExporter,
		instanceID:   instanceID,
		internal:     internal,
		envID:        envID,
		teamID:       teamID,
		cpuMax:       cpuMax,
		memoryMiBMax: memoryMax,
	}
}

func (l *SandboxLogger) sendEvent(logger *zerolog.Event, format string, v ...interface{}) {
	logger.
		Str("instanceID", l.instanceID).
		Str("envID", l.envID).
		Str("teamID", l.teamID).
		Bool("internal", l.internal). // if this is true, it's sent to internal loki else to grafana cloud
		Msgf(format, v...)
}

func (l *SandboxLogger) GetInternalLogger() *SandboxLogger {
	if l.internal {
		return l
	}

	return NewSandboxLogger(l.instanceID, l.envID, l.teamID, l.cpuMax, l.memoryMiBMax, true)
}

func (l *SandboxLogger) Errorf(
	format string,
	v ...interface{},
) {
	l.sendEvent(l.exporter.logger.Error(), format, v...)
}

func (l *SandboxLogger) Warnf(
	format string,
	v ...interface{},
) {
	l.sendEvent(l.exporter.logger.Warn(), format, v...)
}

func (l *SandboxLogger) Infof(
	format string,
	v ...interface{},
) {
	l.sendEvent(l.exporter.logger.Info(), format, v...)
}

func (l *SandboxLogger) Debugf(
	format string,
	v ...interface{},
) {
	l.sendEvent(l.exporter.logger.Debug(), format, v...)
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
			Float64("cpuUsage", cpu).
			Int32("cpuCount", l.cpuMax).
			Msgf("Sandbox is using %d %% of total CPU", int(cpu/float64(l.cpuMax)*100))
	} else if l.cpuWasAboveTreshold.Load() && cpu <= cpuUsageThreshold*float64(l.cpuMax) {
		l.cpuWasAboveTreshold.Store(false)
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpuUsage", cpu).
			Int32("cpuCount", l.cpuMax).
			Msgf("Sandbox usage fell below %d %% of total cpu", int(cpuUsageThreshold*100))
	}
}

func (l *SandboxLogger) MemoryUsage(memoryMB float64) {
	// Cap at memoryMBMax
	memoryMB = math.Min(memoryMB, float64(l.memoryMiBMax))
	if memoryMB > memoryUsageThreshold*float64(l.memoryMiBMax) && int32(memoryMB) > l.memoryWasAbove.Load() {
		l.memoryWasAbove.Store(int32(memoryMB))
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("memoryMBUsed", memoryMB).
			Int32("memoryMBTotal", l.memoryMiBMax).
			Msgf("Sandbox memory used %d %% of RAM", int(memoryMB/float64(l.memoryMiBMax)*100))
		return
	}
}

func (l *SandboxLogger) Signal(sig os.Signal) {
	l.exporter.logger.Info().
		Str("instanceID", l.instanceID).
		Str("envID", l.envID).
		Str("teamID", l.teamID).
		Str("signal", sig.String()).
		Msg("Signal received")
}

func (l *SandboxLogger) CPUPct(cpuPct float64) {
	l.exporter.logger.Info().
		Str("instanceID", l.instanceID).
		Str("envID", l.envID).
		Str("teamID", l.teamID).
		Float64("cpuPct", cpuPct).
		Int32("cpuCount", l.cpuMax).
		Msg("CPU usage")
}

func (l *SandboxLogger) MemMiB(memTotalMiB uint64, memUsedMiB uint64) {
	// Calculate memory used by firecracker
	fcMemUsedMiB := uint64(l.memoryMiBMax) - memTotalMiB

	// Add firecracker memory to the reported memory used for a more accurate value
	realMemUsedMiB := memUsedMiB + fcMemUsedMiB

	l.exporter.logger.Info().
		Str("instanceID", l.instanceID).
		Str("envID", l.envID).
		Str("teamID", l.teamID).
		Uint64("memUsedMiB", memUsedMiB).
		Uint64("memTotalMiB", memTotalMiB).
		Int32("realMemTotalMiB", l.memoryMiBMax).
		Uint64("fcMemUsedMiB", fcMemUsedMiB).
		Uint64("realMemUsedMiB", realMemUsedMiB).
		Msg("Memory usage")
}

func (l *SandboxLogger) Healthcheck(ok bool, alwaysReport bool) {
	if !ok && !l.healthCheckWasFailing.Load() {
		l.healthCheckWasFailing.Store(true)

		l.exporter.logger.Error().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Bool("healthcheck", ok).
			Msg("Sandbox healthcheck started failing")
		return
	}
	if ok && l.healthCheckWasFailing.Load() {
		l.healthCheckWasFailing.Store(false)

		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Bool("healthcheck", ok).
			Msg("Sandbox healthcheck recovered")

		return
	}

	if alwaysReport {
		var msg string
		var logEvent *zerolog.Event
		if ok {
			msg = "Control sandbox healthcheck was successful"
			logEvent = l.exporter.logger.Info()
		} else {
			msg = "Control sandbox healthcheck failed"
			logEvent = l.exporter.logger.Error()
		}

		logEvent.
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Bool("healthcheck", ok).
			Msg(msg)
	}
}
