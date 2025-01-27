package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/jellydator/ttlcache/v3"
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
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ServiceName = "orchestrator"
const ExitErrorExpiration = 60 * time.Second

type server struct {
	orchestrator.UnimplementedSandboxServiceServer
	sandboxes         *smap.Map[*sandbox.Sandbox]
	sandboxExitErrors *ttlcache.Cache[string, error]
	dns               *dns.OrchDNS
	tracer            trace.Tracer
	networkPool       *network.Pool
	templateCache     *template.Cache

	pauseMu sync.Mutex
}

func New() (*grpc.Server, error) {
	ctx := context.Background()

	sandboxExitErrors := ttlcache.New(ttlcache.WithTTL[string, error](ExitErrorExpiration))
	sandboxErrorChecker := func(sandboxID string) error {
		item := sandboxExitErrors.Get(sandboxID)
		return item.Value()
	}

	dnsServer := dns.New(sandboxErrorChecker)
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

	s := grpc.NewServer(
		grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
		),
	)

	orchestrator.RegisterSandboxServiceServer(s, &server{
		tracer:            otel.Tracer(ServiceName),
		dns:               dnsServer,
		sandboxes:         smap.New[*sandbox.Sandbox](),
		sandboxExitErrors: sandboxExitErrors,
		networkPool:       networkPool,
		templateCache:     templateCache,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	return s, nil
}
