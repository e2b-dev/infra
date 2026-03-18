package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// uploadedBuildHeaders stores serialized V4 headers for a completed upload,
// so that peers can transition from P2P reads to storage reads.
const uploadedBuildsTTL = 1 * time.Hour

type uploadedBuildHeaders struct {
	memfileHeader []byte
	rootfsHeader  []byte
}

type Server struct {
	orchestrator.UnimplementedSandboxServiceServer
	orchestrator.UnimplementedChunkServiceServer

<<<<<<< HEAD
	config            cfg.Config
	sandboxFactory    *sandbox.Factory
	info              *service.ServiceInfo
	sandboxes         *sandbox.Map
	proxy             *proxy.SandboxProxy
	networkPool       *network.Pool
	templateCache     *template.Cache
	pauseMu           sync.Mutex
	devicePool        *nbd.DevicePool
	persistence       storage.StorageProvider
	featureFlags      *featureflags.Client
	sbxEventsService  *events.EventsService
	startingSandboxes *semaphore.Weighted
	peerRegistry      peerclient.Registry
	uploadedBuilds    *ttlcache.Cache[string, *uploadedBuildHeaders]
=======
	config                cfg.Config
	sandboxFactory        *sandbox.Factory
	info                  *service.ServiceInfo
	proxy                 *proxy.SandboxProxy
	networkPool           *network.Pool
	templateCache         *template.Cache
	pauseMu               sync.Mutex
	devicePool            *nbd.DevicePool
	persistence           storage.StorageProvider
	featureFlags          *featureflags.Client
	sbxEventsService      *events.EventsService
	startingSandboxes     *semaphore.Weighted
	peerRegistry          peerclient.Registry
	uploadedBuilds        *ttlcache.Cache[string, struct{}]
	sandboxCreateDuration metric.Int64Histogram
>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0
}

type ServiceConfig struct {
	Config           cfg.Config
	Tel              *telemetry.Client
	NetworkPool      *network.Pool
	DevicePool       *nbd.DevicePool
	TemplateCache    *template.Cache
	Info             *service.ServiceInfo
	Proxy            *proxy.SandboxProxy
	SandboxFactory   *sandbox.Factory
	Persistence      storage.StorageProvider
	FeatureFlags     *featureflags.Client
	SbxEventsService *events.EventsService
	PeerRegistry     peerclient.Registry
}

<<<<<<< HEAD
func New(ctx context.Context, cfg ServiceConfig) *Server {
	uploadedBuilds := ttlcache.New(
		ttlcache.WithTTL[string, *uploadedBuildHeaders](uploadedBuildsTTL),
=======
func New(cfg ServiceConfig) (*Server, error) {
	uploadedBuilds := ttlcache.New[string, struct{}](
		ttlcache.WithTTL[string, struct{}](uploadedBuildsTTL),
>>>>>>> f0933bad7768f85e3541c68aa6f07632e159d7c0
	)
	go uploadedBuilds.Start()

	server := &Server{
		config:            cfg.Config,
		sandboxFactory:    cfg.SandboxFactory,
		info:              cfg.Info,
		proxy:             cfg.Proxy,
		networkPool:       cfg.NetworkPool,
		templateCache:     cfg.TemplateCache,
		devicePool:        cfg.DevicePool,
		persistence:       cfg.Persistence,
		featureFlags:      cfg.FeatureFlags,
		sbxEventsService:  cfg.SbxEventsService,
		startingSandboxes: semaphore.NewWeighted(maxStartingInstancesPerNode),
		peerRegistry:      cfg.PeerRegistry,
		uploadedBuilds:    uploadedBuilds,
	}

	meter := cfg.Tel.MeterProvider.Meter("orchestrator.sandbox")

	sandboxCreateDuration, err := telemetry.GetHistogram(meter, telemetry.OrchestratorSandboxCreateDurationName)
	if err != nil {
		return nil, fmt.Errorf("failed to register sandbox create duration histogram: %w", err)
	}
	server.sandboxCreateDuration = sandboxCreateDuration

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorSandboxCountMeterName, func(_ context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(server.sandboxFactory.Sandboxes.Count()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to register sandbox count metric: %w", err)
	}

	return server, nil
}
