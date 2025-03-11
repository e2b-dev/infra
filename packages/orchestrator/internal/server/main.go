package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"math"
	"net"
	"os"
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ServiceName = "orchestrator"

type server struct {
	orchestrator.UnimplementedSandboxServiceServer
	sandboxes       *smap.Map[*sandbox.Sandbox]
	dns             *dns.DNS
	proxy           *proxy.SessionProxy
	tracer          trace.Tracer
	networkPool     *network.Pool
	templateCache   *template.Cache
	pauseMu         sync.Mutex
	clientID        string // nomad node id
	devicePool      *nbd.DevicePool
	clickhouseStore chdb.Store

	useLokiMetrics       string
	useClickhouseMetrics string
}

type Service struct {
	server   *server
	grpc     *grpc.Server
	dns      *dns.DNS
	proxy    *proxy.SessionProxy
	port     uint16
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
}

func New(ctx context.Context, port uint, clientID string, proxy *proxy.SessionProxy) (*Service, error) {
	if port > math.MaxUint16 {
		return nil, fmt.Errorf("%d is larger than maximum possible port %d", port, math.MaxInt16)
	}

	if clientID == "" {
		return nil, errors.New("clientID is required")
	}

	srv := &Service{port: uint16(port)}

	templateCache, err := template.NewCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	networkPool, err := network.NewPool(ctx, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to create network pool: %w", err)
	}

	// BLOCK: initialize services
	{
		srv.dns = dns.New()
		srv.proxy = proxy

		opts := []logging.Option{
			logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
			logging.WithLevels(logging.DefaultServerCodeToLevel),
			logging.WithFieldsFromContext(logging.ExtractFields),
		}
		srv.grpc = grpc.NewServer(
			grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
			grpc.ChainUnaryInterceptor(
				recovery.UnaryServerInterceptor(),
				selector.UnaryServerInterceptor(
					logging.UnaryServerInterceptor(logger.GRPCLogger(zap.L()), opts...),
					logger.WithoutHealthCheck(),
				),
			),
			grpc.ChainStreamInterceptor(
				selector.StreamServerInterceptor(
					logging.StreamServerInterceptor(logger.GRPCLogger(zap.L()), opts...),
					logger.WithoutHealthCheck(),
				),
			),
		)

		devicePool, err := nbd.NewDevicePool()

		if err != nil {
			return nil, fmt.Errorf("failed to create device pool: %w", err)
		}

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
			tracer:               otel.Tracer(ServiceName),
			dns:                  srv.dns,
			proxy:                srv.proxy,
			sandboxes:            smap.New[*sandbox.Sandbox](),
			networkPool:          networkPool,
			templateCache:        templateCache,
			clientID:             clientID,
			devicePool:           devicePool,
			clickhouseStore:      clickhouseStore,
			useLokiMetrics:       useLokiMetrics,
			useClickhouseMetrics: useClickhouseMetrics,
		}
	}

	orchestrator.RegisterSandboxServiceServer(srv.grpc, srv.server)
	grpc_health_v1.RegisterHealthServer(srv.grpc, health.NewServer())

	return srv, nil
}

// Start launches
func (srv *Service) Start(context.Context) error {
	if srv.server == nil || srv.dns == nil || srv.grpc == nil {
		return errors.New("orchestrator services are not initialized")
	}

	go func() {
		zap.L().Info("Starting DNS server")
		if err := srv.dns.Start("127.0.0.4", 53); err != nil {
			zap.L().Fatal("Failed running DNS server", zap.Error(err))
		}
	}()

	// the listener is closed by the shutdown operation
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", srv.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", srv.port, err)
	}

	zap.L().Info("Starting orchestrator server", zap.Uint16("port", srv.port))

	go func() {
		if err := srv.grpc.Serve(lis); err != nil {
			zap.L().Fatal("grpc server failed to serve", zap.Error(err))
		}
	}()

	srv.shutdown.op = func(ctx context.Context) error {
		var errs []error

		srv.grpc.GracefulStop()

		if err := lis.Close(); err != nil {
			errs = append(errs, err)
		}

		if err := srv.dns.Close(ctx); err != nil {
			errs = append(errs, err)
		}

		return errors.Join(errs...)
	}

	return nil
}

func (srv *Service) Close(ctx context.Context) error {
	srv.shutdown.once.Do(func() {
		if srv.shutdown.op == nil {
			// should only be true if there was an error
			// during startup.
			return
		}

		srv.shutdown.err = srv.shutdown.op(ctx)
		srv.shutdown.op = nil
	})
	return srv.shutdown.err
}
