package grpcserver

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"github.com/soheilhy/cmux"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"

	e2bhealthcheck "github.com/e2b-dev/infra/packages/orchestrator/internal/healthcheck"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type GRPCServer struct {
	info *service.ServiceInfo

	grpc       *grpc.Server
	grpcHealth *health.Server

	shutdown struct {
		once sync.Once
		op   func(context.Context) error
		err  error
	}
}

func New(info *service.ServiceInfo) *GRPCServer {
	opts := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
		logging.WithLevels(logging.DefaultServerCodeToLevel),
		logging.WithFieldsFromContext(logging.ExtractFields),
	}

	ignoredLoggingRoutes := logger.WithoutRoutes(
		logger.HealthCheckRoute,
		"/TemplateService/TemplateBuildStatus",
		"/TemplateService/HealthStatus",
	)
	srv := grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second, // Minimum time between pings from client
			PermitWithoutStream: true,            // Allow pings even when no active streams
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    15 * time.Second, // Server sends keepalive pings every 15s
			Timeout: 5 * time.Second,  // Wait 5s for response before considering dead
		}),
		grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
			selector.UnaryServerInterceptor(
				logging.UnaryServerInterceptor(logger.GRPCLogger(zap.L()), opts...),
				ignoredLoggingRoutes,
			),
		),
		grpc.ChainStreamInterceptor(
			selector.StreamServerInterceptor(
				logging.StreamServerInterceptor(logger.GRPCLogger(zap.L()), opts...),
				ignoredLoggingRoutes,
			),
		),
	)

	grpcHealth := health.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, grpcHealth)

	return &GRPCServer{
		info:       info,
		grpc:       srv,
		grpcHealth: grpcHealth,
	}
}

func (g *GRPCServer) HealthServer() *health.Server {
	return g.grpcHealth
}

func (g *GRPCServer) GRPCServer() *grpc.Server {
	return g.grpc
}

// Start launches
func (g *GRPCServer) Start(ctx context.Context, port uint) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	healthcheck, err := e2bhealthcheck.NewHealthcheck(g.info)
	if err != nil {
		return fmt.Errorf("failed to create healthcheck: %w", err)
	}

	// Reuse the same TCP port between grpc and HTTP requests
	m := cmux.New(lis)
	// Match HTTP requests.
	httpL := m.Match(cmux.HTTP1Fast())
	// Match gRPC requests.
	grpcL := m.Match(cmux.Any())

	zap.L().Info("Starting GRPC server", zap.Uint("port", port))

	go func() {
		if err := g.grpc.Serve(grpcL); err != nil {
			zap.L().Fatal("grpc server failed to serve", zap.Error(err))
		}
	}()

	// Start health check
	go healthcheck.Start(ctx, httpL)

	g.shutdown.op = func(ctx context.Context) error {
		// mark services as unhealthy so now new request will be accepted
		select {
		case <-ctx.Done():
			g.grpc.Stop()
		default:
			g.grpc.GracefulStop()
		}
		m.Close()

		return lis.Close()
	}

	// Start serving traffic, blocking call
	return m.Serve()
}

func (g *GRPCServer) Close(ctx context.Context) error {
	g.shutdown.once.Do(func() {
		if g.shutdown.op == nil {
			// should only be true if there was an error
			// during startup.
			return
		}

		g.shutdown.err = g.shutdown.op(ctx)
		g.shutdown.op = nil
	})
	return g.shutdown.err
}
