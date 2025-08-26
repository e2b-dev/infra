package server

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/event"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type server struct {
	orchestrator.UnimplementedSandboxServiceServer

	info                *service.ServiceInfo
	sandboxes           *smap.Map[*sandbox.Sandbox]
	proxy               *proxy.SandboxProxy
	tracer              trace.Tracer
	networkPool         *network.Pool
	templateCache       *template.Cache
	pauseMu             sync.Mutex
	devicePool          *nbd.DevicePool
	persistence         storage.StorageProvider
	featureFlags        *featureflags.Client
	sandboxEventBatcher batcher.ClickhouseInsertBatcher[clickhouse.SandboxEvent]
}

type Service struct {
	info     *service.ServiceInfo
	server   *server
	proxy    *proxy.SandboxProxy
	shutdown struct {
		once sync.Once
		op   func(context.Context) error
		err  error
	}

	persistence storage.StorageProvider
}

type ServiceConfig struct {
	GRPC             *grpcserver.GRPCServer
	Tel              *telemetry.Client
	NetworkPool      *network.Pool
	DevicePool       *nbd.DevicePool
	TemplateCache    *template.Cache
	Tracer           trace.Tracer
	Info             *service.ServiceInfo
	Proxy            *proxy.SandboxProxy
	Sandboxes        *smap.Map[*sandbox.Sandbox]
	Persistence      storage.StorageProvider
	FeatureFlags     *featureflags.Client
	SbxEventsService events.EventsService[event.SandboxEvent]
}

func New(
	ctx context.Context,
	cfg ServiceConfig,
) (*Service, error) {
	srv := &Service{
		info:        cfg.Info,
		proxy:       cfg.Proxy,
		persistence: cfg.Persistence,
	}
	srv.server = &server{
		info:             cfg.Info,
		tracer:           cfg.Tracer,
		proxy:            srv.proxy,
		sandboxes:        cfg.Sandboxes,
		networkPool:      cfg.NetworkPool,
		templateCache:    cfg.TemplateCache,
		devicePool:       cfg.DevicePool,
		persistence:      cfg.Persistence,
		featureFlags:     cfg.FeatureFlags,
		sbxEventsService: cfg.SbxEventsService,
	}

	meter := cfg.Tel.MeterProvider.Meter("orchestrator.sandbox")
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorSandboxCountMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(srv.server.sandboxes.Count()))

		return nil
	})
	if err != nil {
		zap.L().Error("Error registering sandbox count metric", zap.Any("metric_name", telemetry.OrchestratorSandboxCountMeterName), zap.Error(err))
	}

	orchestrator.RegisterSandboxServiceServer(cfg.GRPC.GRPCServer(), srv.server)

	return srv, nil
}
