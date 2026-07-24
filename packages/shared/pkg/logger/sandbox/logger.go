package sbxlogger

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type SandboxLoggerConfig struct {
	// ServiceName is the name of the service that the logger is being created for.
	// The service name is added to every log entry.
	ServiceName string
	// IsInternal differentiates between our (internal) logs, and user accessible (external) logs.
	// For external logger, we also disable stacktraces
	IsInternal       bool
	CollectorAddress string
	// FeatureFlags, when set for an external logger with a CollectorAddress,
	// enables LaunchDarkly-controlled log write routing (see
	// featureflags.LogsWriteConfigFlag). When nil the logger keeps writing only
	// to CollectorAddress (legacy behavior).
	FeatureFlags *featureflags.Client
}

func NewLogger(ctx context.Context, loggerProvider log.LoggerProvider, config SandboxLoggerConfig) logger.Logger {
	level := zap.NewAtomicLevelAt(zap.DebugLevel)

	enableConsole := false
	var core zapcore.Core
	if !config.IsInternal && config.CollectorAddress != "" {
		// Add Vector exporter to the core
		vectorEncoder := zapcore.NewJSONEncoder(GetSandboxEncoderConfig())
		httpWriter := newExternalLogWriter(ctx, config)
		httpCore := zapcore.NewCore(
			vectorEncoder,
			httpWriter,
			level,
		)
		otelCore := logger.GetOTELCore(loggerProvider, config.ServiceName)
		dualWrite := featureflags.NewLogsDualWriteResolver(config.FeatureFlags)
		core = newDualWriteCore(
			func() bool { return dualWrite.Resolve(ctx) },
			httpCore,
			otelCore,
		)
	} else {
		core = logger.GetOTELCore(loggerProvider, config.ServiceName)
		enableConsole = true
	}

	lg, err := logger.NewLogger(logger.LoggerConfig{
		ServiceName:       config.ServiceName,
		IsInternal:        config.IsInternal,
		IsDebug:           true,
		DisableStacktrace: !config.IsInternal,
		InitialFields: []zap.Field{
			zap.String("logger", config.ServiceName),
		},
		Cores:         []zapcore.Core{core},
		EnableConsole: enableConsole,
	})
	if err != nil {
		panic(err)
	}

	return lg
}

// newExternalLogWriter builds the write syncer for external sandbox logs.
// With a FeatureFlags client it resolves destinations from
// featureflags.LogsWriteConfigFlag on each write, falling back to
// CollectorAddress; without one it uses the legacy fixed-address writer.
func newExternalLogWriter(ctx context.Context, config SandboxLoggerConfig) zapcore.WriteSyncer {
	return logger.NewHTTPWriter(ctx, config.CollectorAddress)
}

type dualWriteCore struct {
	resolve boolResolver
	http    zapcore.Core
	otlp    zapcore.Core
	both    zapcore.Core
}

type boolResolver func() bool

func newDualWriteCore(
	resolve boolResolver,
	httpCore zapcore.Core,
	otlpCore zapcore.Core,
) *dualWriteCore {
	return &dualWriteCore{
		resolve: resolve,
		http:    httpCore,
		otlp:    otlpCore,
		both:    zapcore.NewTee(httpCore, otlpCore),
	}
}

func (c *dualWriteCore) Enabled(level zapcore.Level) bool {
	return c.http.Enabled(level)
}

func (c *dualWriteCore) With(fields []zapcore.Field) zapcore.Core {
	return newDualWriteCore(c.resolve, c.http.With(fields), c.otlp.With(fields))
}

func (c *dualWriteCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return c.selected().Check(entry, checked)
}

func (c *dualWriteCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	return c.selected().Write(entry, fields)
}

func (c *dualWriteCore) Sync() error {
	return errors.Join(c.http.Sync(), c.otlp.Sync())
}

func (c *dualWriteCore) selected() zapcore.Core {
	if c.resolve() {
		return c.both
	}

	return c.http
}

func GetSandboxEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:       "timestamp",
		MessageKey:    "message",
		LevelKey:      "level",
		EncodeLevel:   zapcore.LowercaseLevelEncoder,
		NameKey:       "logger",
		StacktraceKey: "stacktrace",
		EncodeTime:    zapcore.RFC3339NanoTimeEncoder,
		LineEnding:    zapcore.DefaultLineEnding,
	}
}
