package factories

import (
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func NewGRPCServer(tel *telemetry.Client) *grpc.Server {
	loggingOpts := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
		logging.WithLevels(logging.DefaultServerCodeToLevel),
		logging.WithFieldsFromContext(logging.ExtractFields),
	}

	// Keep this in sync with orchestrator's gRPC server factory to avoid drift.
	ignoredLoggingRoutes := logger.WithoutRoutes(
		logger.HealthCheckRoute,
		"/TemplateService/TemplateBuildStatus",
		"/TemplateService/HealthStatus",
		"/InfoService/ServiceInfo",
	)

	return grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second, // Minimum time between pings from client
			PermitWithoutStream: true,            // Allow pings even when no active streams
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    15 * time.Second, // Server sends keepalive pings every 15s
			Timeout: 5 * time.Second,  // Wait 5s for response before considering dead
		}),
		grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler(
			otelgrpc.WithTracerProvider(tel.TracerProvider),
			otelgrpc.WithMeterProvider(tel.MeterProvider),
		))),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
			selector.UnaryServerInterceptor(
				logging.UnaryServerInterceptor(logger.GRPCLogger(logger.L()), loggingOpts...),
				ignoredLoggingRoutes,
			),
		),
		grpc.ChainStreamInterceptor(
			selector.StreamServerInterceptor(
				logging.StreamServerInterceptor(logger.GRPCLogger(logger.L()), loggingOpts...),
				ignoredLoggingRoutes,
			),
		),
	)
}
