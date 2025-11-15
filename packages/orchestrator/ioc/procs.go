package ioc

import (
	"context"
	"log"
	"os"
	"slices"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.uber.org/fx"
)

func NewSandboxLoggerInternal(lc fx.Lifecycle, tel *telemetry.Client, state State) {
	sbxLoggerInternal := sbxlogger.NewLogger(
		context.Background(),
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      state.ServiceName,
			IsInternal:       true,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := sbxLoggerInternal.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down sandbox internal logger: %v", err)
				return err
			}

			return nil
		},
	})
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)
}

func NewSandboxLoggerExternal(lc fx.Lifecycle, tel *telemetry.Client, state State) {
	sbxLoggerExternal := sbxlogger.NewLogger(
		context.Background(),
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      state.ServiceName,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := sbxLoggerExternal.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down sandbox external logger: %v", err)
				return err
			}

			return nil
		},
	})
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)
}

func NewDrainingHandler(
	lc fx.Lifecycle,
	serviceInfo *service.ServiceInfo,
) {
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			// Mark service draining if not already.
			// If service stats was previously changed via API, we don't want to override it.
			if serviceInfo.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
				serviceInfo.SetStatus(orchestratorinfo.ServiceInfoStatus_Draining)
			}
			return nil
		},
	})
}

func NewSingleOrchestratorCheck(
	lc fx.Lifecycle,
	config cfg.Config,
	state State,
) {
	// Check if the orchestrator crashed and restarted
	// Skip this check in development mode
	// We don't want to lock if the service is running with force stop; the subsequent start would fail.
	if !env.IsDevelopment() && !config.ForceStop && slices.Contains(state.Services, cfg.Orchestrator) {
		fileLockName := config.OrchestratorLockPath
		info, err := os.Stat(fileLockName)
		if err == nil {
			log.Fatalf("Orchestrator was already started at %s, exiting", info.ModTime())
		}

		f, err := os.Create(fileLockName)
		if err != nil {
			log.Fatalf("Failed to create lock file %s: %v", fileLockName, err)
		}
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				fileErr := f.Close()
				if fileErr != nil {
					log.Printf("Failed to close lock file %s: %v", fileLockName, fileErr)
				}

				// TODO: DO ONLY ON GRACEUL SHUTDOWN
				// Remove the lock file on graceful shutdown
				if fileErr = os.Remove(fileLockName); fileErr != nil {
					log.Printf("Failed to remove lock file %s: %v", fileLockName, fileErr)
				}
				return nil
			},
		})
	}
}
