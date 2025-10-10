package server

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/events/event"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type server struct {
	orchestrator.UnimplementedSandboxServiceServer

	metricsTracker   *metrics.Tracker
	sandboxFactory   *sandbox.Factory
	info             *service.ServiceInfo
	sandboxes        *sandbox.Map
	proxy            *proxy.SandboxProxy
	networkPool      *network.Pool
	templateCache    *template.Cache
	pauseMu          sync.Mutex
	devicePool       *nbd.DevicePool
	persistence      storage.StorageProvider
	featureFlags     *featureflags.Client
	sbxEventsService events.EventsService[event.SandboxEvent]
}

type Service struct {
	info   *service.ServiceInfo
	server *server
	proxy  *proxy.SandboxProxy

	persistence storage.StorageProvider
}

type ServiceConfig struct {
	GRPC             *grpcserver.GRPCServer
	Tel              *telemetry.Client
	NetworkPool      *network.Pool
	DevicePool       *nbd.DevicePool
	TemplateCache    *template.Cache
	Info             *service.ServiceInfo
	MetricsTracker   *metrics.Tracker
	Proxy            *proxy.SandboxProxy
	SandboxFactory   *sandbox.Factory
	Sandboxes        *sandbox.Map
	Persistence      storage.StorageProvider
	FeatureFlags     *featureflags.Client
	SbxEventsService events.EventsService[event.SandboxEvent]
}

func New(cfg ServiceConfig) *Service {
	srv := &Service{
		info:        cfg.Info,
		proxy:       cfg.Proxy,
		persistence: cfg.Persistence,
	}
	srv.server = &server{
		sandboxFactory:   cfg.SandboxFactory,
		info:             cfg.Info,
		proxy:            srv.proxy,
		sandboxes:        cfg.Sandboxes,
		networkPool:      cfg.NetworkPool,
		templateCache:    cfg.TemplateCache,
		devicePool:       cfg.DevicePool,
		persistence:      cfg.Persistence,
		featureFlags:     cfg.FeatureFlags,
		sbxEventsService: cfg.SbxEventsService,
		metricsTracker:   cfg.MetricsTracker,
	}

	meter := cfg.Tel.MeterProvider.Meter("orchestrator.sandbox")
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorSandboxCountMeterName, func(_ context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(srv.server.sandboxes.Count()))

		return nil
	})
	if err != nil {
		zap.L().Error("Error registering sandbox count metric", zap.String("metric_name", string(telemetry.OrchestratorSandboxCountMeterName)), zap.Error(err))
	}

	orchestrator.RegisterSandboxServiceServer(cfg.GRPC.GRPCServer(), srv.server)

	return srv
}
