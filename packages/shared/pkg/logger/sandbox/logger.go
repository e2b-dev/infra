package sbxlogger

import (
	"context"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	sandboxLoggerInternal LoggerBuilder
	sandboxLoggerExternal LoggerBuilder
)

type HealthCheckAction int

const (
	Success HealthCheckAction = iota
	Fail
	ReportSuccess
	ReportFail
)

type LoggerBuilder interface {
	WithMetadata(m LoggerMetadata) Logger
}

type Logger interface {
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)

	Metrics(metrics SandboxMetricsFields)
	Healthcheck(action HealthCheckAction)
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
	logger *zap.Logger
}

type loggerBuilderBase struct {
	logger *zap.Logger
}

func (sl *loggerBuilderBase) WithMetadata(m LoggerMetadata) Logger {
	return &sandboxLogger{
		logger: sl.logger.With(m.LoggerMetadata().Fields()...),
	}
}

func (sl *loggerBuilderBase) Sync() error {
	return sl.logger.Sync()
}

func newSandboxLogger(ctx context.Context, config SandboxLoggerConfig) LoggerBuilder {
	level := zap.NewAtomicLevelAt(zap.DebugLevel)

	core := zapcore.NewNopCore()
	if !config.IsInternal && config.CollectorAddress != "" {
		// Add Vector exporter to the core
		vectorEncoder := zapcore.NewJSONEncoder(logger.GetEncoderConfig(zapcore.DefaultLineEnding))
		httpWriter := logger.NewBufferedHTTPWriter(ctx, config.CollectorAddress)
		core = zapcore.NewCore(
			vectorEncoder,
			httpWriter,
			level,
		)
	} else {
		core = logger.GetOTELCore(config.ServiceName)
	}

	lg, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   config.ServiceName,
		IsInternal:    config.IsInternal,
		IsDevelopment: config.IsDevelopment,
		IsDebug:       true,
		InitialFields: []zap.Field{
			zap.String("logger", config.ServiceName),
		},
		Cores: []zapcore.Core{core},
	})
	if err != nil {
		panic(err)
	}

	return &loggerBuilderBase{
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

func (sl *sandboxLogger) Healthcheck(action HealthCheckAction) {
	switch {
	case action == Success:
		sl.logger.Info("Sandbox healthcheck recovered",
			zap.Bool("healthcheck", true))
	case action == Fail:
		sl.logger.Error("Sandbox healthcheck started failing",
			zap.Bool("healthcheck", false))
	case action == ReportSuccess:
		sl.logger.Info(
			"Control sandbox healthcheck was successful",
			zap.Bool("healthcheck", true))
	case action == ReportFail:
		sl.logger.Error("Control sandbox healthcheck was unsuccessful",
			zap.Bool("healthcheck", false))
	}
}
