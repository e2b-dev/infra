package ioc

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func startDevicePool(lc fx.Lifecycle, s fx.Shutdowner, devicePool *nbd.DevicePool, logger *logger.TracedLogger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			invokeAsync(s, func() error {
				devicePool.Populate(ctx)

				return nil
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info(ctx, "closing device pool")
			defer logger.Info(ctx, "device pool closed")

			return devicePool.Close(ctx)
		},
	})
}

func startNetworkPool(lc fx.Lifecycle, s fx.Shutdowner, networkPool *network.Pool, logger *logger.TracedLogger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			invokeAsync(s, func() error {
				networkPool.Populate(ctx)

				return nil
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info(ctx, "closing network pool")
			defer logger.Info(ctx, "network pool closed")

			return networkPool.Close(ctx)
		},
	})
}

func startSandboxProxy(lc fx.Lifecycle, s fx.Shutdowner, proxy *proxy.SandboxProxy, logger *logger.TracedLogger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			invokeAsync(s, func() error {
				err := proxy.Start(ctx)
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}

				return err
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info(ctx, "closing sandbox proxy")
			defer logger.Info(ctx, "sandbox proxy closed")

			return proxy.Close(ctx)
		},
	})
}
