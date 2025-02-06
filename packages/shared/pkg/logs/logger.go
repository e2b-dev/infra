package logs

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/zap"

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

var CollectorAddress = os.Getenv("LOGS_COLLECTOR_ADDRESS")
var CollectorPublicIP = os.Getenv("LOGS_COLLECTOR_PUBLIC_IP")

func newSandboxLogExporter(serviceName string) *sandboxLogExporter {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	ctx := context.Background()
	exporters := []io.Writer{exporter.NewHTTPLogsExporter(ctx, CollectorAddress)}

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
	cpuMax                int64
	cpuWasAboveTreshold   atomic.Bool
	memoryMiBMax          int64
	memoryWasAbove        atomic.Int32
	healthCheckWasFailing atomic.Bool
}

func NewSandboxLogger(
	instanceID string,
	envID string,
	teamID string,
	cpuMax int64,
	memoryMax int64,
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

// getMessage returns a formatted message from a template and arguments.
// Source: zap.SugaredLogger
func getMessage(template string, fmtArgs []interface{}) string {
	if len(fmtArgs) == 0 {
		return template
	}

	if template != "" {
		return fmt.Sprintf(template, fmtArgs...)
	}

	if len(fmtArgs) == 1 {
		if str, ok := fmtArgs[0].(string); ok {
			return str
		}
	}
	return fmt.Sprint(fmtArgs...)
}

func (l *SandboxLogger) sendEvent(logger *zerolog.Event, format string, v ...interface{}) {
	logger.
		Str("instanceID", l.instanceID).
		Str("envID", l.envID).
		Str("teamID", l.teamID).
		Bool("internal", l.internal). // if this is true, it's sent to internal loki else to grafana cloud
		Msg(getMessage(format, v))
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

func (l *SandboxLogger) Debug(args ...interface{}) {
	l.sendEvent(l.exporter.logger.Debug(), "", args...)
}

func (l *SandboxLogger) Debugf(
	format string,
	v ...interface{},
) {
	l.sendEvent(l.exporter.logger.Debug(), format, v...)
}

func (l *SandboxLogger) Error(args ...interface{}) {
	l.sendEvent(l.exporter.logger.Error(), "", args...)
}

func (l *SandboxLogger) Errorw(msg string, keysAndValues ...interface{}) {
	l.sendEvent(l.exporter.logger.Error(), msg, keysAndValues...)
}

func (l *SandboxLogger) Warn(args ...interface{}) {
	l.sendEvent(l.exporter.logger.Warn(), "", args...)
}

func (l *SandboxLogger) Info(args ...interface{}) {
	l.sendEvent(l.exporter.logger.Info(), "", args...)
}

func (l *SandboxLogger) Panic(args ...interface{}) {
	l.sendEvent(l.exporter.logger.Panic(), "", args...)
}

func (l *SandboxLogger) Fatalf(format string, v ...interface{}) {
	l.sendEvent(l.exporter.logger.Fatal(), format, v...)
}

func (l *SandboxLogger) Desugar() *zap.Logger {
	l.Panic("Desugar is not implemented for SandboxLogger")
	return nil
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
			Int64("cpuCount", l.cpuMax).
			Msgf("Sandbox is using %d %% of total CPU", int(cpu/float64(l.cpuMax)*100))
	} else if l.cpuWasAboveTreshold.Load() && cpu <= cpuUsageThreshold*float64(l.cpuMax) {
		l.cpuWasAboveTreshold.Store(false)
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("cpuUsage", cpu).
			Int64("cpuCount", l.cpuMax).
			Msgf("Sandbox usage fell below %d %% of total cpu", int(cpuUsageThreshold*100))
	}
}

func (l *SandboxLogger) MemoryUsage(memoryMiB float64) {
	// Cap at memoryMBMax
	memoryMiB = math.Min(memoryMiB, float64(l.memoryMiBMax))
	if memoryMiB > memoryUsageThreshold*float64(l.memoryMiBMax) && int32(memoryMiB) > l.memoryWasAbove.Load() {
		l.memoryWasAbove.Store(int32(memoryMiB))
		l.exporter.logger.Warn().
			Str("instanceID", l.instanceID).
			Str("envID", l.envID).
			Str("teamID", l.teamID).
			Float64("memoryMiBUsed", memoryMiB).
			Int64("memoryMiBTotal", l.memoryMiBMax).
			Msgf("Sandbox memory used %d %% of RAM", int(memoryMiB/float64(l.memoryMiBMax)*100))
		return
	}
}

func (l *SandboxLogger) Metrics(memTotalMiB, memUsedMiB uint64, cpuCount uint32, cpuUsedPct float32) {
	l.exporter.logger.Info().
		Str("category", "metrics").
		Str("instanceID", l.instanceID).
		Str("envID", l.envID).
		Str("teamID", l.teamID).
		Float32("cpuUsedPct", cpuUsedPct).
		Uint32("cpuCount", cpuCount).
		Uint64("memTotalMiB", memTotalMiB).
		Uint64("memUsedMiB", memUsedMiB).
		Msg("Metrics")

	return
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
