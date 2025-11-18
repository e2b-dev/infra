package ioc

import (
	"context"
	"log"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func newSandboxLoggerInternal(lc fx.Lifecycle, tel *telemetry.Client, state State) {
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
		OnStop: func(context.Context) error {
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

func newSandboxLoggerExternal(lc fx.Lifecycle, tel *telemetry.Client, state State) {
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
		OnStop: func(context.Context) error {
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
