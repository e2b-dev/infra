package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const metricExportPeriod = 5 * time.Second

type server struct {
	orchestrator.UnimplementedSandboxServiceServer

	info            *service.ServiceInfo
	sandboxes       *smap.Map[*sandbox.Sandbox]
	proxy           *proxy.SandboxProxy
	tracer          trace.Tracer
	networkPool     *network.Pool
	templateCache   *template.Cache
	pauseMu         sync.Mutex
	devicePool      *nbd.DevicePool
	persistence     storage.StorageProvider
	sandboxObserver *telemetry.SandboxObserver
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

func New(
	ctx context.Context,
	grpc *grpcserver.GRPCServer,
	tel *telemetry.Client,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	tracer trace.Tracer,
	info *service.ServiceInfo,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
) (*Service, error) {
	srv := &Service{info: info}

	templateCache, err := template.NewCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	// BLOCK: initialize services
	{
		srv.proxy = proxy

		persistence, err := storage.GetTemplateStorageProvider(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create storage provider: %w", err)
		}

		srv.persistence = persistence

		sandboxObserver, err := telemetry.NewSandboxObserver(ctx, info.SourceCommit, info.ClientId)
		if err != nil {
			return nil, fmt.Errorf("failed to create sandbox observer: %w", err)
		}

		srv.server = &server{
			info:            info,
			tracer:          tracer,
			proxy:           srv.proxy,
			sandboxes:       sandboxes,
			networkPool:     networkPool,
			templateCache:   templateCache,
			devicePool:      devicePool,
			sandboxObserver: sandboxObserver,
			persistence:     persistence,
		}

		meter := tel.MeterProvider.Meter("orchestrator.sandbox")
		_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorSandboxCountMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
			observer.Observe(int64(srv.server.sandboxes.Count()))

			return nil
		})

		if err != nil {
			zap.L().Error("Error registering sandbox count metric", zap.Any("metric_name", telemetry.OrchestratorSandboxCountMeterName), zap.Error(err))
		}
	}

	orchestrator.RegisterSandboxServiceServer(grpc.GRPCServer(), srv.server)

	return srv, nil
}
