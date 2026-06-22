//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

// uploadDrainLogInterval is how often Close logs progress while waiting for
// in-flight snapshot uploads to finish during shutdown.
const uploadDrainLogInterval = 10 * time.Second

const sandboxDrainPollInterval = 5 * time.Second

func sandboxDrainLogInterval(elapsed time.Duration) time.Duration {
	switch {
	case elapsed < time.Minute:
		return 5 * time.Second
	case elapsed < time.Hour:
		return time.Minute
	default:
		return 15 * time.Minute
	}
}

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
	uploadFailedCounter   metric.Int64Counter

	// uploadsWG tracks in-flight async snapshot uploads so a graceful shutdown
	// can wait for them to finish instead of dropping them. uploadsInFlight is
	// the live count, used to log drain progress during shutdown.
	uploadsWG       sync.WaitGroup
	uploadsInFlight atomic.Int64
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

	uploadFailedCounter, err := telemetry.GetCounter(meter, telemetry.OrchestratorSnapshotUploadFailedCounterName)
	if err != nil {
		return nil, fmt.Errorf("failed to register snapshot upload failed counter: %w", err)
	}
	server.uploadFailedCounter = uploadFailedCounter

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
				attribute.String("status", server.info.GetStatus().Status.String()),
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

func (s *Server) Close(ctx context.Context) error {
	s.StartDraining(ctx)

	// Wait for in-flight snapshot uploads to finish so a graceful shutdown
	// doesn't drop a snapshot that is still uploading. ctx is cancelled on a
	// forced stop, in which case we stop waiting and let the process exit.
	uploadsDone := make(chan struct{})
	go func() {
		s.uploadsWG.Wait()
		close(uploadsDone)
	}()

	s.drainUploads(ctx, uploadsDone)

	s.uploadedBuilds.Stop()

	return nil
}

// drainUploads waits for in-flight snapshot uploads to finish, logging progress
// periodically, until they complete or ctx is cancelled (forced stop).
func (s *Server) drainUploads(ctx context.Context, uploadsDone <-chan struct{}) {
	inFlight := s.uploadsInFlight.Load()
	if inFlight == 0 {
		return
	}

	logger.L().Info(ctx, "waiting for in-flight snapshot uploads to finish", zap.Int64("uploads", inFlight))

	ticker := time.NewTicker(uploadDrainLogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-uploadsDone:
			logger.L().Info(ctx, "all in-flight snapshot uploads finished")

			return
		case <-ctx.Done():
			logger.L().Warn(ctx, "shutting down with snapshot uploads still in flight",
				zap.Int64("uploads", s.uploadsInFlight.Load()),
				zap.Error(context.Cause(ctx)),
			)

			return
		case <-ticker.C:
			logger.L().Info(ctx, "still waiting for in-flight snapshot uploads",
				zap.Int64("uploads", s.uploadsInFlight.Load()),
			)
		}
	}
}

func (s *Server) StartDraining(ctx context.Context) {
	// A single factory drain gate is shared by API sandboxes and template-build
	// sandboxes. The server's Create/Checkpoint handlers hold this gate across
	// the whole operation, so there is no separate server-level gate.
	s.sandboxFactory.StartDraining(ctx)
}

func (s *Server) DrainSandboxes(ctx context.Context) error {
	s.StartDraining(ctx)

	// Create/Checkpoint hold the factory gate for their whole operation —
	// including the checkpoint's internal resume that runs after the old sandbox
	// is removed — so waiting for the gate to drain is enough before snapshotting
	// the live set: no admitted start can still be mid-flight afterward.
	if err := s.sandboxFactory.WaitSandboxStarts(ctx); err != nil {
		return err
	}

	live := s.sandboxFactory.Sandboxes.Count()
	logger.L().Info(ctx, "starting graceful sandbox drain", zap.Int("live_sandboxes", live))

	ticker := time.NewTicker(sandboxDrainPollInterval)
	defer ticker.Stop()
	startedAt := time.Now()
	lastLoggedAt := startedAt

	for {
		remaining := s.sandboxFactory.Sandboxes.Count()
		if remaining == 0 {
			logger.L().Info(ctx, "graceful sandbox drain complete", zap.Int("live_sandboxes", remaining))

			return s.waitSandboxLifecycles(ctx)
		}

		select {
		case <-ctx.Done():
			logger.L().Warn(ctx, "graceful sandbox drain timed out",
				zap.Int("remaining_sandboxes", remaining),
				zap.Error(ctx.Err()),
			)

			return ctx.Err()
		case <-ticker.C:
			now := time.Now()
			remaining = s.sandboxFactory.Sandboxes.Count()
			elapsed := now.Sub(startedAt)
			if remaining > 0 && now.Sub(lastLoggedAt) >= sandboxDrainLogInterval(elapsed) {
				logger.L().Info(ctx, "waiting for sandbox drain",
					zap.Int("remaining_sandboxes", remaining),
					zap.Duration("elapsed", elapsed),
				)
				lastLoggedAt = now
			}
		}
	}
}

func (s *Server) ForceStopSandboxes(ctx context.Context) error {
	// Drain the shared factory gate so no new starts can enter while shutdown proceeds.
	s.StartDraining(ctx)
	// stopped dedups across the loop's repeated passes so a lifecycle that is
	// still present in successive snapshots is only force-closed once.
	stopped := make(map[string]struct{})
	var errs []error

	cleanupCtx := context.WithoutCancel(ctx)
	forceStop := func(sandboxes []*sandbox.Sandbox) error {
		var wg sync.WaitGroup
		errCh := make(chan error, len(sandboxes))
		started := 0

		for _, sbx := range sandboxes {
			key := fmt.Sprintf("%s/%s", sbx.Runtime.SandboxID, sbx.LifecycleID)
			if _, ok := stopped[key]; ok {
				continue
			}
			stopped[key] = struct{}{}
			started++

			wg.Go(func() {
				sbxLog := logger.L().With(
					logger.WithSandboxID(sbx.Runtime.SandboxID),
					logger.WithLifecycleID(sbx.LifecycleID),
					logger.WithSandboxIP(sbx.Slot.HostIPString()),
				)
				sbxLog.Warn(cleanupCtx, "force stopping sandbox during orchestrator shutdown")

				marked := s.sandboxFactory.Sandboxes.MarkStopping(cleanupCtx, sbx.Runtime.SandboxID, sbx.LifecycleID)
				if !marked {
					sbxLog.Info(cleanupCtx, "sandbox was already removed from live map before force stop")
				}

				if err := sbx.Close(cleanupCtx); err != nil {
					errCh <- fmt.Errorf("close sandbox %s/%s: %w", sbx.Runtime.SandboxID, sbx.LifecycleID, err)
					sbxLog.Error(cleanupCtx, "failed to force close sandbox", zap.Error(err))
				}

				sbxLog.Info(cleanupCtx, "forced sandbox cleanup complete")
			})
		}

		if started == 0 {
			return nil
		}

		closeErrs, err := collectForceStopSandboxCloseErrors(ctx, &wg, errCh)
		errs = append(errs, closeErrs...)
		if err != nil {
			return err
		}

		return nil
	}

	logger.L().Warn(ctx, "starting forced sandbox shutdown",
		zap.Int("sandbox_count", len(s.sandboxFactory.Sandboxes.LifecycleItems())),
	)

	// Forced shutdown must make progress immediately: close already-running
	// sandboxes right away rather than first blocking on in-flight starts, so one
	// slow or stuck start cannot starve cleanup of live workloads. Then wait for
	// in-flight starts to finish and do a final pass for any sandbox that became
	// visible during the wait. forceStop dedups by sandbox/lifecycle so each is
	// closed only once. forceStop returns a non-nil error only on context
	// cancellation (close failures are accumulated into errs), so short-circuit
	// the wait and final pass in that case.
	if err := forceStop(s.sandboxFactory.Sandboxes.LifecycleItems()); err != nil {
		errs = append(errs, err)
	} else if err := s.sandboxFactory.WaitSandboxStarts(ctx); err != nil {
		errs = append(errs, err)
	} else if err := forceStop(s.sandboxFactory.Sandboxes.LifecycleItems()); err != nil {
		errs = append(errs, err)
	}

	if err := s.waitSandboxLifecycles(ctx); err != nil {
		errs = append(errs, err)
	}

	if err := errors.Join(errs...); err != nil {
		logger.L().Error(ctx, "forced sandbox shutdown finished with errors", zap.Error(err))

		return err
	}

	logger.L().Info(ctx, "forced sandbox shutdown complete")

	return nil
}

func collectForceStopSandboxCloseErrors(ctx context.Context, wg *sync.WaitGroup, errCh chan error) ([]error, error) {
	if err := utils.WaitGroupWait(ctx, wg); err != nil {
		return drainForceStopSandboxCloseErrors(errCh), fmt.Errorf("force stopping sandbox cleanup: %w", err)
	}

	close(errCh)

	return drainForceStopSandboxCloseErrors(errCh), nil
}

func drainForceStopSandboxCloseErrors(errCh <-chan error) []error {
	var errs []error
	for {
		select {
		case err, ok := <-errCh:
			if !ok {
				return errs
			}
			errs = append(errs, err)
		default:
			return errs
		}
	}
}

func (s *Server) waitSandboxLifecycles(ctx context.Context) error {
	return s.sandboxFactory.Sandboxes.WaitLifecycles(ctx)
}

func (s *Server) refreshStartingSandboxesLimit(ctx context.Context) {
	ticker := time.NewTicker(startingSandboxesLimitRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.sandboxFactory.Done():
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
