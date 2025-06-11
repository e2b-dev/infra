package server

import (
	"context"
	"fmt"
	"os"
	"sync"

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
	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

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
	clickhouseStore chdb.Store
	persistence     storage.StorageProvider

	useLokiMetrics       string
	useClickhouseMetrics string
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
	// there really should be a config struct for this
	// using something like viper to read the config
	// but for now this is just a quick hack
	// see https://linear.app/e2b/issue/E2B-1731/use-viper-to-read-env-vars
	useLokiMetrics       string
	useClickhouseMetrics string

	persistence storage.StorageProvider
}

func New(
	ctx context.Context,
	grpc *grpcserver.GRPCServer,
	telemetryClient *telemetry.Client,
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

		useLokiMetrics := os.Getenv("WRITE_LOKI_METRICS")
		useClickhouseMetrics := os.Getenv("WRITE_CLICKHOUSE_METRICS")
		readClickhouseMetrics := os.Getenv("READ_CLICKHOUSE_METRICS")

		var clickhouseStore chdb.Store = nil

		if readClickhouseMetrics == "true" || useClickhouseMetrics == "true" {
			clickhouseStore, err = chdb.NewStore(chdb.ClickHouseConfig{
				ConnectionString: os.Getenv("CLICKHOUSE_CONNECTION_STRING"),
				Username:         os.Getenv("CLICKHOUSE_USERNAME"),
				Password:         os.Getenv("CLICKHOUSE_PASSWORD"),
				Database:         os.Getenv("CLICKHOUSE_DATABASE"),
				Debug:            os.Getenv("CLICKHOUSE_DEBUG") == "true",
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create clickhouse store: %w", err)
			}
		}

		srv.server = &server{
			info:                 info,
			tracer:               tracer,
			proxy:                srv.proxy,
			sandboxes:            sandboxes,
			networkPool:          networkPool,
			templateCache:        templateCache,
			devicePool:           devicePool,
			clickhouseStore:      clickhouseStore,
			useLokiMetrics:       useLokiMetrics,
			useClickhouseMetrics: useClickhouseMetrics,
			persistence:          persistence,
		}

		meter := telemetryClient.MeterProvider.Meter("orchestrator")
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
