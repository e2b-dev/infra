package ioc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/factories"
	e2bhealthcheck "github.com/e2b-dev/infra/packages/orchestrator/internal/healthcheck"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/soheilhy/cmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc/health"
)

func NewHealthModule() fx.Option {
	return fx.Module("health",
		fx.Provide(
			newGRPCHealthServer,
			newHealthHTTPServer,
		),
		fx.Invoke(
			startHealthHTTPServer, // Health HTTP server
		),
	)
}

func newGRPCHealthServer(
	logger *zap.Logger,
) *health.Server {
	s := health.NewServer()
	logger.Info("Registered gRPC service", zap.String("service", "grpc.health.v1.Health"))
	return s
}

// HealthHTTPServer wraps the health check HTTP server to distinguish it from HyperloopHTTPServer in DI
type HealthHTTPServer struct {
	*http.Server
}

func newHealthHTTPServer(
	lc fx.Lifecycle,
	cmuxServer cmux.CMux,
	serviceInfo *service.ServiceInfo,
	logger *zap.Logger,
) (HealthHTTPServer, error) {
	httpListener := cmuxServer.Match(cmux.HTTP1Fast())
	healthcheck, err := e2bhealthcheck.NewHealthcheck(serviceInfo)
	if err != nil {
		return HealthHTTPServer{}, fmt.Errorf("failed to create healthcheck: %w", err)
	}

	httpServer := factories.NewHTTPServer()
	httpServer.Handler = healthcheck.CreateHandler()

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				err := httpServer.Serve(httpListener)
				if err != nil && !errors.Is(err, cmux.ErrServerClosed) && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("HTTP server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("Shutting down http server")
			return httpServer.Shutdown(ctx)
		},
	})

	return HealthHTTPServer{httpServer}, nil
}

func startHealthHTTPServer(server HealthHTTPServer) {}
