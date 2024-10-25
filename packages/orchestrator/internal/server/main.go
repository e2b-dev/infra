package server

import (
	"context"
	"log"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	consulapi "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	ipSlotPoolSize       = 32
	reusedIpSlotPoolSize = 64
)

type server struct {
	orchestrator.UnimplementedSandboxServer
	sandboxes   *smap.Map[*sandbox.Sandbox]
	dns         *dns.DNS
	tracer      trace.Tracer
	consul      *consulapi.Client
	networkPool *sandbox.NetworkSlotPool
}

func New() *grpc.Server {
	s := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithInterceptorFilter(filters.Not(filters.HealthCheck())))),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
		),
	)

	log.Println("Initializing orchestrator server")

	ctx := context.Background()

	dnsServer := dns.New()
	go dnsServer.Start("127.0.0.1:53")

	tracer := otel.Tracer(constants.ServiceName)

	consulClient, err := consul.New(ctx)
	if err != nil {
		panic(err)
	}

	networkPool := sandbox.NewNetworkSlotPool(ipSlotPoolSize, reusedIpSlotPoolSize)

	// We start the pool last to avoid allocation network slots if the other components fail to initialize.
	go func() {
		poolErr := networkPool.Start(ctx, consulClient)
		if poolErr != nil {
			log.Fatalf("network pool error: %v\n", poolErr)
		}
	}()

	orchestrator.RegisterSandboxServer(s, &server{
		tracer:      tracer,
		consul:      consulClient,
		dns:         dnsServer,
		sandboxes:   smap.New[*sandbox.Sandbox](),
		networkPool: networkPool,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	return s
}
