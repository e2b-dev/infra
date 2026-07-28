package grpc

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// ServerOption configures NewGRPCServer.
type ServerOption func(*serverOptions)

type serverOptions struct {
	withSandboxResumeMetrics bool
	certFile                 string
	keyFile                  string
	certPEM                  []byte
	keyPEM                   []byte
}

// WithSandboxResumeMetrics adds sandbox.resume attribute to otelgrpc metrics,
// read from incoming gRPC metadata.
func WithSandboxResumeMetrics() ServerOption {
	return func(o *serverOptions) { o.withSandboxResumeMetrics = true }
}

// WithTLS configures server-side TLS using the given certificate and key files.
func WithTLS(certFile, keyFile string) ServerOption {
	return func(o *serverOptions) {
		o.certFile = certFile
		o.keyFile = keyFile
	}
}

// WithTLSFromPEM configures server-side TLS using in-memory PEM-encoded certificate and key.
func WithTLSFromPEM(certPEM, keyPEM []byte) ServerOption {
	return func(o *serverOptions) {
		o.certPEM = certPEM
		o.keyPEM = keyPEM
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

	serverOpts := []grpc.ServerOption{
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
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
			selector.UnaryServerInterceptor(
				logging.UnaryServerInterceptor(logger.GRPCLogger(logger.L()), logOpts...),
				ignoredLoggingRoutes,
			),
		),
		grpc.ChainStreamInterceptor(
			selector.StreamServerInterceptor(
				logging.StreamServerInterceptor(logger.GRPCLogger(logger.L()), logOpts...),
				ignoredLoggingRoutes,
			),
		),
	}

	if cfg.certFile != "" && cfg.keyFile != "" {
		creds, err := credentials.NewServerTLSFromFile(cfg.certFile, cfg.keyFile)
		if err != nil {
			logger.L().Fatal(context.Background(), "failed to load gRPC TLS credentials",
				zap.String("certFile", cfg.certFile),
				zap.String("keyFile", cfg.keyFile),
				zap.Error(err),
			)
		}

		serverOpts = append(serverOpts, grpc.Creds(creds))
	} else if len(cfg.certPEM) > 0 && len(cfg.keyPEM) > 0 {
		cert, err := tls.X509KeyPair(cfg.certPEM, cfg.keyPEM)
		if err != nil {
			logger.L().Fatal(context.Background(), "failed to parse gRPC TLS PEM credentials", zap.Error(err))
		}

		creds := credentials.NewServerTLSFromCert(&cert)
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}

	return grpc.NewServer(serverOpts...)
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
