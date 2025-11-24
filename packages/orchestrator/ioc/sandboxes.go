package ioc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func newSandboxesModule() fx.Option {
	return fx.Module("sandboxes",
		fx.Provide(
			newDevicePool,
			asGRPCRegisterable(newOrchestratorService),
			newSandboxFactory,
			newSandboxProxy,
			newSandboxesMap,
		),
	)
}

func newSandboxesMap() *sandbox.Map {
	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	return sandbox.NewSandboxesMap()
}

func newOrchestratorService(
	sandboxFactory *sandbox.Factory,
	tel *telemetry.Client,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	templateCache *template.Cache,
	sandboxProxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	persistence storage.StorageProvider,
	featureFlags *featureflags.Client,
	sbxEventsService *events.EventsService,
	serviceInfo *service.ServiceInfo,
) (grpcRegisterable, error) {
	s, err := server.New(server.ServiceConfig{
		SandboxFactory:   sandboxFactory,
		Tel:              tel,
		NetworkPool:      networkPool,
		DevicePool:       devicePool,
		TemplateCache:    templateCache,
		Info:             serviceInfo,
		Proxy:            sandboxProxy,
		Sandboxes:        sandboxes,
		Persistence:      persistence,
		FeatureFlags:     featureFlags,
		SbxEventsService: sbxEventsService,
	})
	if err != nil {
		return grpcRegisterable{}, fmt.Errorf("failed to create server: %w", err)
	}

	return grpcRegisterable{func(g *grpc.Server) {
		orchestrator.RegisterSandboxServiceServer(g, s)
	}}, nil
}

type sandboxFactoryOut struct {
	fx.Out

	*sandbox.Factory
	CMUXWaitBeforeShutdown `group:"cmux-waitable"`
}

func newSandboxFactory(
	logger *zap.Logger,
	config cfg.Config,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	featureFlags *featureflags.Client,
) sandboxFactoryOut {
	factory := sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, featureFlags)

	return sandboxFactoryOut{
		Factory: factory,
		CMUXWaitBeforeShutdown: cmuxWaitBeforeShutdown{fn: func(context.Context) error {
			logger.Info("waiting for sandboxes to exit ... ")
			factory.Wait()

			return nil
		}},
	}
}

func newDevicePool(
	lc fx.Lifecycle,
	globalLogger *zap.Logger,
) (*nbd.DevicePool, error) {
	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return nil, fmt.Errorf("failed to create device pool: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			globalLogger.Info("Starting NBD device pool")
			go devicePool.Populate(ctx)

			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down NBD device pool")

			return devicePool.Close(ctx)
		},
	})

	return devicePool, nil
}

func newSandboxProxy(
	lc fx.Lifecycle,
	tel *telemetry.Client,
	config cfg.Config,
	sandboxes *sandbox.Map,
	globalLogger *zap.Logger,
) (*proxy.SandboxProxy, error) {
	sandboxProxy, err := proxy.NewSandboxProxy(tel.MeterProvider, config.ProxyPort, sandboxes)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox proxy: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				err := sandboxProxy.Start(ctx)
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					globalLogger.Error("Sandbox proxy error", zap.Error(err))
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down sandbox proxy")

			return sandboxProxy.Close(ctx)
		},
	})

	return sandboxProxy, nil
}
