package server

import (
	"context"
	"log"

	"cloud.google.com/go/storage"

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
	localStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	ipSlotPoolSize       = 32
	reusedIpSlotPoolSize = 64
)

type server struct {
	orchestrator.UnimplementedSandboxServer
	sandboxes     *smap.Map[*sandbox.Sandbox]
	dns           *dns.DNS
	tracer        trace.Tracer
	consul        *consulapi.Client
	networkPool   *network.SlotPool
	templateCache *localStorage.TemplateCache
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

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		log.Fatalf("failed to create GCS client: %v", err)
	}

	networkPool := network.NewSlotPool(ipSlotPoolSize, consulClient)

	// We start the pool last to avoid allocation network slots if the other components fail to initialize.
	go func() {
		poolErr := networkPool.Populate(ctx)
		if poolErr != nil {
			log.Fatalf("network pool error: %v\n", poolErr)
		}
	}()

	if templateStorage.BucketName == "" {
		// TODO: Add helper method with something like Mustk
		log.Fatalf("template storage bucket name is empty")
	}

	templateCache := localStorage.NewTemplateCache(ctx, client, templateStorage.BucketName)

	orchestrator.RegisterSandboxServer(s, &server{
		tracer:        tracer,
		consul:        consulClient,
		dns:           dnsServer,
		sandboxes:     smap.New[*sandbox.Sandbox](),
		networkPool:   networkPool,
		templateCache: templateCache,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	return s
}
