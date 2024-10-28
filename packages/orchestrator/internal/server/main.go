package server

import (
	"context"
	"log"

	"cloud.google.com/go/storage"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	snapshotStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	ServiceName    = "orchestrator"
	ipSlotPoolSize = 32
)

type server struct {
	orchestrator.UnimplementedSandboxServiceServer
	tracer        trace.Tracer
	sandboxes     *smap.Map[*sandbox.Sandbox]
	dns           *dns.DNS
	networkPool   *network.SlotPool
	templateCache *snapshotStorage.TemplateCache
}

func New(logger *zap.Logger) *grpc.Server {
	opts := []grpc_zap.Option{
		logging.WithoutHealthCheck(),
	}

	s := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithInterceptorFilter(filters.Not(filters.HealthCheck())))),
		grpc.ChainUnaryInterceptor(
			grpc_zap.UnaryServerInterceptor(logger, opts...),
			recovery.UnaryServerInterceptor(),
		),
	)

	ctx := context.Background()

	dns := dns.New()
	go func() {
		err := dns.Start("127.0.0.1:53")
		if err != nil {
			log.Fatalf("DNS server error: %v\n", err)
		}
	}()

	tracer := otel.Tracer(ServiceName)

	consulClient, err := consul.New(ctx)
	if err != nil {
		log.Fatalf("failed to create consul client: %v", err)
	}

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		log.Fatalf("failed to create GCS client: %v", err)
	}

	nbdPool, err := nbd.NewDevicePool()
	if err != nil {
		log.Fatalf("failed to create NBD pool: %v", err)
	}

	templateCache := snapshotStorage.NewTemplateCache(
		ctx,
		client,
		templateStorage.BucketName,
		nbdPool,
	)

	networkPool := network.NewSlotPool(ipSlotPoolSize, consulClient)

	// We start the pool last to avoid allocation network slots if the other components fail to initialize.
	go func() {
		poolErr := networkPool.Populate(ctx)
		if poolErr != nil {
			log.Fatalf("network pool error: %v\n", poolErr)
		}
	}()

	orchestrator.RegisterSandboxServiceServer(s, &server{
		tracer:        tracer,
		dns:           dns,
		sandboxes:     smap.New[*sandbox.Sandbox](),
		networkPool:   networkPool,
		templateCache: templateCache,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	return s
}
