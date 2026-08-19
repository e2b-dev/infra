package grpc

import (
	"context"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// ServerOption configures NewGRPCServer.
type ServerOption func(*serverOptions)

type serverOptions struct {
	withSandboxResumeMetrics bool
	recoveryHandler          recovery.RecoveryHandlerFunc
	unaryDeadline            grpc.UnaryServerInterceptor
}

// WithSandboxResumeMetrics adds sandbox.resume attribute to otelgrpc metrics,
// read from incoming gRPC metadata.
func WithSandboxResumeMetrics() ServerOption {
	return func(o *serverOptions) { o.withSandboxResumeMetrics = true }
}

// WithRecoveryHandler configures the unary panic recovery handler.
func WithRecoveryHandler(handler recovery.RecoveryHandlerFunc) ServerOption {
	return func(o *serverOptions) { o.recoveryHandler = handler }
}

// WithUnaryDeadline bounds unary requests while preserving an earlier caller deadline.
func WithUnaryDeadline(timeout time.Duration) ServerOption {
	return func(o *serverOptions) {
		o.unaryDeadline = func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			return handler(ctx, req)
		}
	}
}

func NewGRPCServer(tel *telemetry.Client, opts ...ServerOption) *grpc.Server {
	var cfg serverOptions
	for _, o := range opts {
		o(&cfg)
	}

	logOpts := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
		logging.WithLevels(logging.DefaultServerCodeToLevel),
		logging.WithFieldsFromContext(logging.ExtractFields),
	}

	ignoredLoggingRoutes := logger.WithoutRoutes(
		logger.HealthCheckRoute,
		"/TemplateService/TemplateBuildStatus",
		"/TemplateService/HealthStatus",
		"/InfoService/ServiceInfo",
	)

	otelOpts := []otelgrpc.Option{
		otelgrpc.WithTracerProvider(tel.TracerProvider),
		otelgrpc.WithMeterProvider(tel.MeterProvider),
	}
	if cfg.withSandboxResumeMetrics {
		otelOpts = append(otelOpts, otelgrpc.WithMetricAttributesFn(extractSandboxResumeAttrs))
	}

	var recoveryOpts []recovery.Option
	if cfg.recoveryHandler != nil {
		recoveryOpts = append(recoveryOpts, recovery.WithRecoveryHandler(cfg.recoveryHandler))
	}

	unaryInterceptors := []grpc.UnaryServerInterceptor{
		recovery.UnaryServerInterceptor(recoveryOpts...),
		selector.UnaryServerInterceptor(
			logging.UnaryServerInterceptor(logger.GRPCLogger(logger.L()), logOpts...),
			ignoredLoggingRoutes,
		),
	}
	if cfg.unaryDeadline != nil {
		unaryInterceptors = append(unaryInterceptors, cfg.unaryDeadline)
	}

	return grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    15 * time.Second,
			Timeout: 5 * time.Second,
		}),
		grpc.StatsHandler(
			NewStatsWrapper(
				otelgrpc.NewServerHandler(otelOpts...))),
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(
			selector.StreamServerInterceptor(
				logging.StreamServerInterceptor(logger.GRPCLogger(logger.L()), logOpts...),
				ignoredLoggingRoutes,
			),
		),
	)
}

// extractSandboxResumeAttrs reads sandbox.resume from gRPC metadata set by the
// API client. Called by otelgrpc during TagRPC — before the request payload is
// deserialized — so we use metadata instead of the payload.
func extractSandboxResumeAttrs(ctx context.Context) []attribute.KeyValue {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}

	values := md.Get(IsResumeMetadataKey)
	if len(values) == 0 {
		return nil
	}

	return []attribute.KeyValue{
		attribute.Bool("sandbox.resume", values[0] == "true"),
	}
}
