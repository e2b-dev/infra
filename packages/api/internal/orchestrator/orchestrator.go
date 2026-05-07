package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/api/internal/metrics"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/evictor"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations"
	redisreservations "github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations/redis"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/populate_redis"
	redisbackend "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const statusLogInterval = time.Second * 20

var ErrNodeNotFound = errors.New("node not found")

// SnapshotCacheInvalidator invalidates cached snapshot entries.
type SnapshotCacheInvalidator interface {
	Invalidate(ctx context.Context, sandboxID string)
}

type Orchestrator struct {
	httpClient                    *http.Client
	nomadClient                   *nomadapi.Client
	nomadNodePool                 string
	sandboxStore                  *sandbox.Store
	nodes                         *smap.Map[*nodemanager.Node]
	placementAlgorithm            *placement.BestOfK
	featureFlagsClient            *featureflags.Client
	analytics                     *analyticscollector.Analytics
	posthogClient                 *analyticscollector.PosthogClient
	routingCatalog                e2bcatalog.SandboxesCatalog
	sqlcDB                        *sqlcdb.Client
	tel                           *telemetry.Client
	clusters                      *clusters.Pool
	metricsRegistration           metric.Registration
	sandboxCountGaugeRegistration metric.Registration
	createdSandboxesCounter       metric.Int64Counter
	teamMetricsObserver           *metrics.TeamObserver
	accessTokenGenerator          *sandbox.AccessTokenGenerator
	createdCounter                metric.Int64Counter
	snapshotCache                 SnapshotCacheInvalidator

	snapshotUpsertSem *utils.AdjustableSemaphore
	redisStorage      *redisbackend.Storage

	// connectGroup deduplicates concurrent dial+register attempts for the same
	// physical node. It is keyed by NomadNodeShortID (Nomad-managed nodes) or
	// scopedNodeID(clusterID, instanceNodeID) (cluster nodes) and is held inside
	// connectToNode / connectToClusterNode, so it guards every connection path
	// regardless of what triggered the attempt.
	connectGroup singleflight.Group

	// discoveryGroup deduplicates concurrent on-demand discovery attempts in
	// getOrConnectNode that target the same missing orchestrator node. It is
	// intentionally separate from connectGroup to avoid a deadlock: for cluster
	// nodes the outer discoveryGroup key and the inner connectGroup key are the
	// same string, and nesting Do calls for the same key on the same Group would
	// block forever.
	discoveryGroup singleflight.Group
}

func New(
	ctx context.Context,
	config cfg.Config,
	tel *telemetry.Client,
	nomadClient *nomadapi.Client,
	posthogClient *analyticscollector.PosthogClient,
	redisClient redis.UniversalClient,
	sqlcDB *sqlcdb.Client,
	clusters *clusters.Pool,
	featureFlags *featureflags.Client,
	accessTokenGenerator *sandbox.AccessTokenGenerator,
	snapshotCache SnapshotCacheInvalidator,
	snapshotUpsertSem *utils.AdjustableSemaphore,
) (*Orchestrator, error) {
	analyticsInstance, err := analyticscollector.NewAnalytics(
		config.AnalyticsCollectorHost,
		config.AnalyticsCollectorAPIToken,
	)
	if err != nil {
		logger.L().Error(ctx, "Error initializing Analytics client", zap.Error(err))

		return nil, err
	}
	analyticsInstance.Init(ctx)

	var routingCatalog e2bcatalog.SandboxesCatalog
	if redisClient != nil {
		routingCatalog = e2bcatalog.NewRedisSandboxCatalog(redisClient)
	} else {
		routingCatalog = e2bcatalog.NewMemorySandboxesCatalog()
	}

	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	meter := tel.MeterProvider.Meter("github.com/e2b-dev/infra/packages/api/internal/orchestrator")

	createdCounter, err := telemetry.GetCounter(meter, telemetry.SandboxCreateMeterName)
	if err != nil {
		logger.L().Error(ctx, "error getting counter", zap.Error(err))

		return nil, err
	}

	httpClient := &http.Client{
		Timeout: nodeHealthCheckTimeout,
	}

	bestOfKAlgorithm := placement.NewBestOfK(getBestOfKConfig(ctx, featureFlags)).(*placement.BestOfK)

	redisStorage := redisbackend.NewStorage(redisClient)
	go redisStorage.Start(ctx)

	o := Orchestrator{
		httpClient:           httpClient,
		analytics:            analyticsInstance,
		posthogClient:        posthogClient,
		nomadClient:          nomadClient,
		nomadNodePool:        config.NomadNodePool,
		nodes:                smap.New[*nodemanager.Node](),
		placementAlgorithm:   bestOfKAlgorithm,
		featureFlagsClient:   featureFlags,
		accessTokenGenerator: accessTokenGenerator,
		routingCatalog:       routingCatalog,
		sqlcDB:               sqlcDB,
		snapshotCache:        snapshotCache,
		tel:                  tel,
		clusters:             clusters,
		redisStorage:         redisStorage,

		createdCounter: createdCounter,

		snapshotUpsertSem: snapshotUpsertSem,
	}

	var reservationStorage sandbox.ReservationStorage
	var sandboxStorage sandbox.Storage

	switch config.SandboxStorageBackend {
	case cfg.SandboxStorageBackendMemory:
		reservationStorage = reservations.NewReservationStorage()
		sandboxStorage = populate_redis.NewStorage(memory.NewStorage(), redisStorage)
		logger.L().Info(ctx, "Using populate_redis sandbox storage backend")
	case cfg.SandboxStorageBackendRedis:
		reservationStorage = redisreservations.NewReservationStorage(redisClient)
		sandboxStorage = redisStorage
		logger.L().Info(ctx, "Using redis sandbox storage backend")
	default:
		return nil, fmt.Errorf("invalid sandbox storage backend: %s", config.SandboxStorageBackend)
	}

	o.sandboxStore = sandbox.NewStore(
		sandboxStorage,
		reservationStorage,
		sandbox.Callbacks{
			AddSandboxToRoutingTable: o.addSandboxToRoutingTable,
			AsyncNewlyCreatedSandbox: o.handleNewlyCreatedSandbox,
			RemoveSandboxFromNode:    o.killOrphanSandbox,
		},
	)

	// Evict old sandboxes
	sandboxEvictor := evictor.New(o.sandboxStore, o.RemoveSandbox)
	go sandboxEvictor.Start(ctx)

	teamMetricsObserver, err := metrics.NewTeamObserver(ctx, o.sandboxStore)
	if err != nil {
		logger.L().Error(ctx, "Failed to create team metrics observer", zap.Error(err))

		return nil, fmt.Errorf("failed to create team metrics observer: %w", err)
	}

	o.teamMetricsObserver = teamMetricsObserver

	// For local development and testing, we skip the Nomad sync
	// Local cluster is used for single-node setups instead
	skipNomadSync := env.IsLocal()
	go o.keepInSync(ctx, o.sandboxStore, skipNomadSync)

	if err := o.setupMetrics(tel.MeterProvider); err != nil {
		logger.L().Error(ctx, "Failed to setup metrics", zap.Error(err))

		return nil, fmt.Errorf("failed to setup metrics: %w", err)
	}

	go o.startStatusLogging(ctx)
	go o.updateBestOfKConfig(ctx)

	return &o, nil
}

func (o *Orchestrator) startStatusLogging(ctx context.Context) {
	ticker := time.NewTicker(statusLogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.L().Info(ctx, "Stopping status logging")

			return
		case <-ticker.C:
			connectedNodes := make([]map[string]any, 0, o.nodes.Count())

			for _, nodeItem := range o.nodes.Items() {
				if nodeItem == nil {
					connectedNodes = append(connectedNodes, map[string]any{
						"id": "nil",
					})
				} else {
					connectedNodes = append(connectedNodes, map[string]any{
						"id":        nodeItem.ID,
						"status":    nodeItem.Status(),
						"sandboxes": nodeItem.Metrics().SandboxCount,
					})
				}
			}

			logger.L().Info(ctx, "API internal status",
				zap.Int("nodes_count", o.nodes.Count()),
				zap.Any("nodes", connectedNodes),
			)
		}
	}
}

func (o *Orchestrator) Close(ctx context.Context) error {
	var errs []error

	connectedNodes := o.nodes.Items()
	for _, node := range connectedNodes {
		if err := node.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	logger.L().Info(ctx, "Shutting down node clients", zap.Int("error_count", len(errs)), zap.Int("node_count", len(connectedNodes)))

	if o.metricsRegistration != nil {
		if err := o.metricsRegistration.Unregister(); err != nil {
			errs = append(errs, fmt.Errorf("failed to unregister metrics: %w", err))
		}
	}

	if o.sandboxCountGaugeRegistration != nil {
		if err := o.sandboxCountGaugeRegistration.Unregister(); err != nil {
			errs = append(errs, fmt.Errorf("failed to unregister sandbox count gauge: %w", err))
		}
	}

	if o.teamMetricsObserver != nil {
		if err := o.teamMetricsObserver.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to close team metrics observer: %w", err))
		}
	}

	if err := o.analytics.Close(); err != nil {
		errs = append(errs, err)
	}

	if err := o.routingCatalog.Close(ctx); err != nil {
		errs = append(errs, err)
	}

	o.redisStorage.Close()

	return errors.Join(errs...)
}

// updateBestOfKConfig periodically updates the BestOfK algorithm configuration from feature flags
func (o *Orchestrator) updateBestOfKConfig(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Check for config updates every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			config := getBestOfKConfig(ctx, o.featureFlagsClient)

			// Update the config
			o.placementAlgorithm.UpdateConfig(config)
		}
	}
}

func getBestOfKConfig(ctx context.Context, featureFlagsClient *featureflags.Client) placement.BestOfKConfig {
	k := featureFlagsClient.IntFlag(ctx, featureflags.BestOfKSampleSize)

	maxOvercommitPercent := featureFlagsClient.IntFlag(ctx, featureflags.BestOfKMaxOvercommit)

	alphaPercent := featureFlagsClient.IntFlag(ctx, featureflags.BestOfKAlpha)

	canFit := featureFlagsClient.BoolFlag(ctx, featureflags.BestOfKCanFitFlag)

	tooManyStarting := featureFlagsClient.BoolFlag(ctx, featureflags.BestOfKTooManyStartingFlag)

	// Convert percentage to decimal
	alpha := float64(alphaPercent) / 100.0
	maxOvercommit := float64(maxOvercommitPercent) / 100.0

	return placement.BestOfKConfig{
		R:               maxOvercommit,
		K:               k,
		Alpha:           alpha,
		CanFit:          canFit,
		TooManyStarting: tooManyStarting,
	}
}
