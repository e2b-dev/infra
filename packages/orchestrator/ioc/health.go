package ioc

import (
	"fmt"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/factories"
	e2bhealthcheck "github.com/e2b-dev/infra/packages/orchestrator/internal/healthcheck"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
)

func newHealthModule() fx.Option {
	return fx.Module("health",
		fx.Provide(
			asGRPCRegisterable(newGRPCHealthServer),
			newHealthHTTPServer,
		),
		fx.Invoke(
			startHealthHTTPServer, // Health HTTP server
		),
	)
}

func newGRPCHealthServer(logger *zap.Logger) grpcRegisterable {
	s := health.NewServer()
	logger.Info("Registered gRPC service", zap.String("service", "grpc.health.v1.Health"))

	return grpcRegisterable{func(server *grpc.Server) {
		grpc_health_v1.RegisterHealthServer(server, s)
	}}
}

// HealthHTTPServer wraps the health check HTTP server to distinguish it from HyperloopHTTPServer in DI
type HealthHTTPServer struct {
	*http.Server
}

func newHealthHTTPServer(serviceInfo *service.ServiceInfo) (HealthHTTPServer, error) {
	healthcheck, err := e2bhealthcheck.NewHealthcheck(serviceInfo)
	if err != nil {
		return HealthHTTPServer{}, fmt.Errorf("failed to create healthcheck: %w", err)
	}

	httpServer := factories.NewHTTPServer()
	httpServer.Handler = healthcheck.CreateHandler()

	return HealthHTTPServer{httpServer}, nil
}

func startHealthHTTPServer(HealthHTTPServer) {}
