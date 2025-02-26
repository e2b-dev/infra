package server

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	grpc_logging "github.com/grpc-ecosystem/go-grpc-middleware/logging"
	"log"
	"sync"

	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	e2blogging "github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ServiceName = "orchestrator"

type server struct {
	orchestrator.UnimplementedSandboxServiceServer
	sandboxes     *smap.Map[*sandbox.Sandbox]
	dns           *dns.DNS
	tracer        trace.Tracer
	networkPool   *network.Pool
	templateCache *template.Cache

	pauseMu sync.Mutex
}

func New() (*grpc.Server, error) {
	ctx := context.Background()

	dnsServer := dns.New()
	go func() {
		log.Printf("Starting DNS server")

		err := dnsServer.Start("127.0.0.4", 53)
		if err != nil {
			log.Fatalf("Failed running DNS server: %s\n", err.Error())
		}
	}()

	templateCache, err := template.NewCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	networkPool, err := network.NewPool(ctx, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create network pool: %w", err)
	}

	loggerSugar, err := e2blogging.New(env.IsLocal())
	if err != nil {
		return nil, fmt.Errorf("initializing logger: %w", err)
	}
	logger := loggerSugar.Desugar()

	opts := []grpc_zap.Option{e2blogging.WithoutHealthCheck()}
	s := grpc.NewServer(
		grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
			grpc_zap.UnaryServerInterceptor(logger, opts...),
			grpc_zap.PayloadUnaryServerInterceptor(logger, withoutHealthCheckPayload()),
		),
		grpc.ChainStreamInterceptor(
			grpc_zap.StreamServerInterceptor(logger, opts...),
			grpc_zap.PayloadStreamServerInterceptor(logger, withoutHealthCheckPayload()),
		),
	)

	orchestrator.RegisterSandboxServiceServer(s, &server{
		tracer:        otel.Tracer(ServiceName),
		dns:           dnsServer,
		sandboxes:     smap.New[*sandbox.Sandbox](),
		networkPool:   networkPool,
		templateCache: templateCache,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	return s, nil
}

func withoutHealthCheckPayload() grpc_logging.ServerPayloadLoggingDecider {
	return func(ctx context.Context, fullMethodName string, servingObject interface{}) bool {
		// will not log gRPC calls if it was a call to healthcheck and no error was raised
		if fullMethodName == "/grpc.health.v1.Health/Check" {
			return false
		}

		// by default everything will be logged
		return true
	}
}
