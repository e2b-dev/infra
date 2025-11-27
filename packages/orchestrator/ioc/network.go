package ioc

import (
	"context"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func newNetworkModule() fx.Option {
	return fx.Module("network",
		fx.Provide(
			newNetworkPool,
			newNetworkStorage,
		),
		fx.Invoke(
			func(network.Storage) {}, // tell fx that we want to run this
		),
	)
}

func newNetworkStorage(config cfg.Config, state State) (network.Storage, error) {
	if env.IsDevelopment() || config.NetworkConfig.UseLocalNamespaceStorage {
		s, err := network.NewStorageLocal(config.NetworkConfig)
		if err != nil {
			return nil, err
		}

		return s, nil
	}

	return network.NewStorageKV(state.NodeID, config.NetworkConfig)
}

func newNetworkPool(
	lc fx.Lifecycle,
	s fx.Shutdowner,
	config cfg.Config,
	globalLogger logger.Logger,
	storage network.Storage,
) (*network.Pool, error) {
	networkPool := network.NewPool(network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, storage, config.NetworkConfig)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			invokeAsync(s, func() error {
				globalLogger.Info(ctx, "Starting network pool")

				return networkPool.Populate(ctx)
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info(ctx, "Shutting down network pool")

			return networkPool.Close(ctx)
		},
	})

	return networkPool, nil
}
