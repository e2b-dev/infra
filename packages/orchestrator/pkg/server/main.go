//go:build linux

package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/events"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Matches the template cache TTL so entries live as long as the
// templates they refer to and are cleaned up automatically.
const uploadedBuildsTTL = 1 * time.Hour

// startingSandboxesLimitRefreshInterval is how often we re-read the
// MaxStartingInstancesPerNode feature flag and resize the semaphore.
const startingSandboxesLimitRefreshInterval = 30 * time.Second

type Server struct {
	orchestrator.UnimplementedSandboxServiceServer
	orchestrator.UnimplementedChunkServiceServer

	config                cfg.Config
	sandboxFactory        *sandbox.Factory
	info                  *service.ServiceInfo
	proxy                 *proxy.SandboxProxy
	networkPool           *network.Pool
	templateCache         *template.Cache
	devicePool            *nbd.DevicePool
	persistence           storage.StorageProvider
	featureFlags          *featureflags.Client
	sbxEventsService      *events.EventsService
	startingSandboxes     *utils.AdjustableSemaphore
	peerRegistry          peerclient.Registry
	uploadedBuilds        *ttlcache.Cache[string, struct{}]
	uploads               *sandbox.Uploads
	sandboxCreateDuration metric.Int64Histogram
	sandboxKilledCounter  metric.Int64Counter

	done      chan struct{}
	closeOnce sync.Once
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
	Uploads          *sandbox.Uploads
}

func New(ctx context.Context, cfg ServiceConfig) (*Server, error) {
	uploadedBuilds := ttlcache.New[string, struct{}](
		ttlcache.WithTTL[string, struct{}](uploadedBuildsTTL),
	)
	go uploadedBuilds.Start()

	startingLimit := cfg.FeatureFlags.IntFlag(ctx, featureflags.MaxStartingInstancesPerNode)
	startingSandboxes, err := utils.NewAdjustableSemaphore(int64(startingLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to create starting sandboxes semaphore: %w", err)
	}

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
		startingSandboxes: startingSandboxes,
		peerRegistry:      cfg.PeerRegistry,
		uploadedBuilds:    uploadedBuilds,
		uploads:           cfg.Uploads,
		done:              make(chan struct{}),
	}

	meter := cfg.Tel.MeterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/server")

	sandboxCreateDuration, err := telemetry.GetHistogram(meter, telemetry.OrchestratorSandboxCreateDurationName)
	if err != nil {
		return nil, fmt.Errorf("failed to register sandbox create duration histogram: %w", err)
	}
	server.sandboxCreateDuration = sandboxCreateDuration

	sandboxKilledCounter, err := telemetry.GetCounter(meter, telemetry.OrchestratorSandboxKilledCounterName)
	if err != nil {
		return nil, fmt.Errorf("failed to register sandbox kills counter: %w", err)
	}
	server.sandboxKilledCounter = sandboxKilledCounter

	_, err = telemetry.GetObservableUpDownCounter(meter, telemetry.OrchestratorSandboxCountMeterName, func(_ context.Context, observer metric.Int64Observer) error {
		observer.Observe(int64(server.sandboxFactory.Sandboxes.Count()))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to register sandbox count metric: %w", err)
	}

	statusGauge, err := telemetry.GetGaugeInt(meter, telemetry.OrchestratorStatusGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator status gauge: %w", err)
	}

	_, err = meter.RegisterCallback(
		func(_ context.Context, obs metric.Observer) error {
			obs.ObserveInt64(statusGauge, 1, metric.WithAttributes(
				attribute.String("status", server.info.GetStatus().String()),
				attribute.String("version", server.info.SourceVersion),
				attribute.String("commit", server.info.SourceCommit),
			))

			return nil
		}, statusGauge)
	if err != nil {
		return nil, fmt.Errorf("failed to register orchestrator status gauge: %w", err)
	}

	cpuAllocatedGauge, err := telemetry.GetGaugeInt(meter, telemetry.OrchestratorCpuAllocatedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator CPU allocated gauge: %w", err)
	}

	memoryAllocatedGauge, err := telemetry.GetGaugeInt(meter, telemetry.OrchestratorMemoryAllocatedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator memory allocated gauge: %w", err)
	}

	diskAllocatedGauge, err := telemetry.GetGaugeInt(meter, telemetry.OrchestratorDiskAllocatedGaugeName)
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator disk allocated gauge: %w", err)
	}

	_, err = meter.RegisterCallback(
		func(_ context.Context, obs metric.Observer) error {
			var (
				cpuAllocated    int64
				memoryAllocated int64
				diskAllocated   int64
			)

			for _, item := range server.sandboxFactory.Sandboxes.Items() {
				cpuAllocated += item.Config.Vcpu
				memoryAllocated += item.Config.RamMB * 1024 * 1024
				diskAllocated += item.Config.TotalDiskSizeMB * 1024 * 1024
			}

			obs.ObserveInt64(cpuAllocatedGauge, cpuAllocated)
			obs.ObserveInt64(memoryAllocatedGauge, memoryAllocated)
			obs.ObserveInt64(diskAllocatedGauge, diskAllocated)

			return nil
		}, cpuAllocatedGauge, memoryAllocatedGauge, diskAllocatedGauge)
	if err != nil {
		return nil, fmt.Errorf("failed to register orchestrator sandbox resource gauges: %w", err)
	}

	go server.refreshStartingSandboxesLimit(ctx)

	return server, nil
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
	})

	s.uploadedBuilds.Stop()

	return nil
}

func (s *Server) refreshStartingSandboxesLimit(ctx context.Context) {
	ticker := time.NewTicker(startingSandboxesLimitRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			limit := s.featureFlags.IntFlag(ctx, featureflags.MaxStartingInstancesPerNode)
			if limit <= 0 {
				continue
			}

			if err := s.startingSandboxes.SetLimit(int64(limit)); err != nil {
				logger.L().Error(ctx, "failed to adjust starting sandboxes semaphore",
					zap.Int("limit", limit), zap.Error(err))
			}
		}
	}
}
