package server

import (
	"context"
	"log"

	"cloud.google.com/go/storage"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	consulapi "github.com/hashicorp/consul/api"
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
	sandboxes     *smap.Map[*sandbox.Sandbox]
	dns           *dns.DNS
	tracer        trace.Tracer
	consul        *consulapi.Client
	networkPool   *sandbox.NetworkSlotPool
	nbdPool       *nbd.DevicePool
	templateCache *snapshotStorage.TemplateDataCache
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

	log.Println("Initializing orchestrator server")

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

	templateCache := snapshotStorage.NewTemplateDataCache(ctx, client, templateStorage.BucketName)

	nbdPool, err := nbd.NewDevicePool()
	if err != nil {
		log.Fatalf("failed to create NBD pool: %v", err)
	}

	networkPool := sandbox.NewNetworkSlotPool(ipSlotPoolSize)

	go func() {
		poolErr := networkPool.Start(ctx, tracer, consulClient)
		if poolErr != nil {
			log.Fatalf("network pool error: %v\n", poolErr)
		}
	}()

	orchestrator.RegisterSandboxServiceServer(s, &server{
		tracer:        tracer,
		consul:        consulClient,
		dns:           dns,
		sandboxes:     smap.New[*sandbox.Sandbox](),
		networkPool:   networkPool,
		nbdPool:       nbdPool,
		templateCache: templateCache,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	return s
}
