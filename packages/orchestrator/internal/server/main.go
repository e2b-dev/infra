package server

import (
	"context"
	"fmt"
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

	"github.com/e2b-dev/infra/packages/orchestrator/internal/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	snapshotStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	ipSlotPoolSize = 32
	bucket
	ServiceName = "orchestrator"
)

type server struct {
	orchestrator.UnimplementedSandboxServer
	sandboxes     *smap.Map[*sandbox.Sandbox]
	dns           *dns.DNS
	tracer        trace.Tracer
	consul        *consulapi.Client
	networkPool   chan sandbox.IPSlot
	snapshotCache *snapshotStorage.SnapshotDataCache
}

func New(logger *zap.Logger) *grpc.Server {
	opts := []grpc_zap.Option{logging.WithoutHealthCheck()}

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
	go dns.Start("127.0.0.1:53")

	tracer := otel.Tracer(ServiceName)

	consulClient, err := consul.New(ctx)
	if err != nil {
		panic(err)
	}

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		errMsg := fmt.Errorf("failed to create GCS client: %v", err)
		panic(errMsg)
	}

	snapshotCache := snapshotStorage.NewSnapshotDataCache(ctx, client, constants.BucketName)

	// Sandboxes waiting for the network slot can be passed and reschedulede
	// so we should include a FIFO system for waiting.
	networkPool := make(chan sandbox.IPSlot, ipSlotPoolSize)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				ips, err := sandbox.NewSlot(ctx, tracer, consulClient)
				if err != nil {
					logger.Error("failed to create network", zap.Error(err))

					continue
				}

				err = ips.CreateNetwork(ctx, tracer)
				if err != nil {
					ips.Release(ctx, tracer, consulClient)

					logger.Error("failed to create network", zap.Error(err))

					continue
				}

				networkPool <- *ips
			}
		}
	}()

	orchestrator.RegisterSandboxServer(s, &server{
		tracer:        tracer,
		consul:        consulClient,
		dns:           dns,
		sandboxes:     smap.New[*sandbox.Sandbox](),
		networkPool:   networkPool,
		snapshotCache: snapshotCache,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	return s
}
