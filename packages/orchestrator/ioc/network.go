package ioc

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func NewNetworkStorage(config cfg.Config, state State) (network.Storage, error) {
	if env.IsDevelopment() || config.NetworkConfig.UseLocalNamespaceStorage {
		return network.NewStorageLocal(config.NetworkConfig)
	}

	return network.NewStorageKV(state.NodeID, config.NetworkConfig)
}

func NewNetworkPool(
	lc fx.Lifecycle,
	config cfg.Config,
	globalLogger *zap.Logger,
	storage network.Storage,
) (*network.Pool, error) {
	networkPool := network.NewPool(network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, storage, config.NetworkConfig)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			globalLogger.Info("Starting network pool")
			go networkPool.Populate(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down network pool")
			return networkPool.Close(ctx)
		},
	})

	return networkPool, nil
}
