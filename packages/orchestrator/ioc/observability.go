package ioc

import (
	"context"
	"fmt"
	"log"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func newObservabilityModule() fx.Option {
	return fx.Module("observability",
		fx.Provide(
			newTelemetry,
			newGlobalLogger,
			newSandboxObserver,
		),
	)
}

func newTelemetry(lc fx.Lifecycle, state State, version VersionInfo) (*telemetry.Client, error) {
	// Setup telemetry
	tel, err := telemetry.New(context.Background(), state.NodeID, state.ServiceName, version.Commit, version.Version, state.ServiceInstanceID)
	if err != nil {
		return nil, err
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

	return tel, nil
}

func newSandboxObserver(
	lc fx.Lifecycle,
	state State,
	sandboxes *sandbox.Map,
	versionInfo VersionInfo,
) (*metrics.SandboxObserver, error) {
	sandboxObserver, err := metrics.NewSandboxObserver(context.Background(), state.NodeID, state.ServiceName, versionInfo.Commit, versionInfo.Version, state.ServiceInstanceID, sandboxes)
	if err != nil {
		return nil, fmt.Errorf("error creating sandbox observer: %w", err)
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return sandboxObserver.Close(ctx)
		},
	})

	return sandboxObserver, nil
}

func newGlobalLogger(
	lc fx.Lifecycle,
	tel *telemetry.Client,
	state State,
	version VersionInfo,
) (logger.Logger, error) {
	ctx := context.Background()

	globalLogger, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   state.ServiceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, state.ServiceName)},
		EnableConsole: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			err := globalLogger.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down logger: %v", err)

				return err
			}

			return nil
		},
	})
	logger.ReplaceGlobals(ctx, globalLogger)

	globalLogger.Info(ctx, "Starting orchestrator",
		zap.String("version", version.Version),
		zap.String("commit", version.Commit),
		logger.WithServiceInstanceID(state.ServiceInstanceID))

	return globalLogger, nil
}
