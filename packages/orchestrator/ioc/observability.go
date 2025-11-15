package ioc

import (
	"context"
	"log"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func NewTelemetry(lc fx.Lifecycle, state State, version VersionInfo) *telemetry.Client {
	// Setup telemetry
	tel, err := telemetry.New(context.Background(), state.NodeID, state.ServiceName, version.Commit, version.Version, state.ServiceInstanceID)
	if err != nil {
		zap.L().Fatal("failed to init telemetry", zap.Error(err))
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := tel.Shutdown(ctx)
			if err != nil {
				log.Printf("error while shutting down telemetry: %v", err)
				return err
			}
			return nil
		},
	})

	return tel
}

func NewSandboxObserver(
	lc fx.Lifecycle,
	state State,
	sandboxes *sandbox.Map,
	globalLogger *zap.Logger,
	versionInfo VersionInfo,
) (*metrics.SandboxObserver, error) {
	sandboxObserver, err := metrics.NewSandboxObserver(context.Background(), state.NodeID, state.ServiceName, versionInfo.Commit, versionInfo.Version, state.ServiceInstanceID, sandboxes)
	if err != nil {
		globalLogger.Fatal("failed to create sandbox observer", zap.Error(err))
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return sandboxObserver.Close(ctx)
		},
	})

	return sandboxObserver, nil
}

func NewGlobalLogger(
	lc fx.Lifecycle,
	tel *telemetry.Client,
	state State,
	version VersionInfo,
) *zap.Logger {
	globalLogger := zap.Must(logger.NewLogger(context.Background(), logger.LoggerConfig{
		ServiceName:   state.ServiceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, state.ServiceName)},
		EnableConsole: true,
	}))
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := globalLogger.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down logger: %v", err)
				return err
			}

			return nil
		},
	})
	zap.ReplaceGlobals(globalLogger)

	globalLogger.Info("Starting orchestrator",
		zap.String("version", version.Version),
		zap.String("commit", version.Commit),
		logger.WithServiceInstanceID(state.ServiceInstanceID))

	return globalLogger
}
