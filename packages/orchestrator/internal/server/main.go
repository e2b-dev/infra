package server

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Server struct {
	orchestrator.UnimplementedSandboxServiceServer

	config            cfg.Config
	sandboxFactory    *sandbox.Factory
	info              *service.ServiceInfo
	sandboxes         *sandbox.Map
	proxy             *proxy.SandboxProxy
	networkPool       *network.Pool
	templateCache     *template.Cache
	pauseMu           sync.Mutex
	devicePool        *nbd.DevicePool
	persistence       storage.API
	featureFlags      *featureflags.Client
	sbxEventsService  *events.EventsService
	startingSandboxes *semaphore.Weighted
}

type ServiceConfig struct {
	Config           cfg.Config
	Tel              *telemetry.Client
	NetworkPool      *network.Pool
	DevicePool       *nbd.DevicePool
	TemplateCache    *template.Cache
	Info             *service.ServiceInfo
	Proxy            *proxy.SandboxProxy
	SandboxFactory   *sandbox.Factory
	Sandboxes        *sandbox.Map
	Persistence      storage.API
	FeatureFlags     *featureflags.Client
	SbxEventsService *events.EventsService
}

func New(ctx context.Context, cfg ServiceConfig) *Server {
	server := &Server{
		config:            cfg.Config,
		sandboxFactory:    cfg.SandboxFactory,
		info:              cfg.Info,
		proxy:             cfg.Proxy,
		sandboxes:         cfg.Sandboxes,
		networkPool:       cfg.NetworkPool,
		templateCache:     cfg.TemplateCache,
		devicePool:        cfg.DevicePool,
		persistence:       cfg.Persistence,
		featureFlags:      cfg.FeatureFlags,
		sbxEventsService:  cfg.SbxEventsService,
		startingSandboxes: semaphore.NewWeighted(maxStartingInstancesPerNode),
	}

	meter := cfg.Tel.MeterProvider.Meter("orchestrator.sandbox")
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorSandboxCountMeterName, func(_ context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(server.sandboxes.Count()))

		return nil
	})
	if err != nil {
		logger.L().Error(ctx, "Error registering sandbox count metric", zap.String("metric_name", string(telemetry.OrchestratorSandboxCountMeterName)), zap.Error(err))
	}

	return server
}
