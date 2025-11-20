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

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	"github.com/e2b-dev/infra/packages/api/internal/metrics"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/evictor"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/populate_redis"
	redisbackend "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const statusLogInterval = time.Second * 20

var ErrNodeNotFound = errors.New("node not found")

type Orchestrator struct {
	httpClient              *http.Client
	nomadClient             *nomadapi.Client
	sandboxStore            *sandbox.Store
	nodes                   *smap.Map[*nodemanager.Node]
	leastBusyAlgorithm      placement.Algorithm
	bestOfKAlgorithm        *placement.BestOfK
	featureFlagsClient      *featureflags.Client
	analytics               *analyticscollector.Analytics
	posthogClient           *analyticscollector.PosthogClient
	routingCatalog          e2bcatalog.SandboxesCatalog
	dbClient                *db.DB
	sqlcDB                  *sqlcdb.Client
	tel                     *telemetry.Client
	clusters                *edge.Pool
	metricsRegistration     metric.Registration
	createdSandboxesCounter metric.Int64Counter
	teamMetricsObserver     *metrics.TeamObserver
	accessTokenGenerator    *sandbox.AccessTokenGenerator
	sandboxCounter          metric.Int64UpDownCounter
	createdCounter          metric.Int64Counter
}

func New(
	ctx context.Context,
	config cfg.Config,
	tel *telemetry.Client,
	nomadClient *nomadapi.Client,
	posthogClient *analyticscollector.PosthogClient,
	redisClient redis.UniversalClient,
	dbClient *db.DB,
	sqlcDB *sqlcdb.Client,
	clusters *edge.Pool,
	featureFlags *featureflags.Client,
	accessTokenGenerator *sandbox.AccessTokenGenerator,
) (*Orchestrator, error) {
	analyticsInstance, err := analyticscollector.NewAnalytics(
		config.AnalyticsCollectorHost,
		config.AnalyticsCollectorAPIToken,
	)
	if err != nil {
		zap.L().Error("Error initializing Analytics client", zap.Error(err))

		return nil, err
	}

	var routingCatalog e2bcatalog.SandboxesCatalog
	if redisClient != nil {
		routingCatalog = e2bcatalog.NewRedisSandboxesCatalog(redisClient)
	} else {
		routingCatalog = e2bcatalog.NewMemorySandboxesCatalog()
	}

	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	meter := tel.MeterProvider.Meter("api.cache.sandbox")
	sandboxCounter, err := telemetry.GetUpDownCounter(meter, telemetry.SandboxCountMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))

		return nil, err
	}

	createdCounter, err := telemetry.GetCounter(meter, telemetry.SandboxCreateMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))

		return nil, err
	}

	httpClient := &http.Client{
		Timeout: nodeHealthCheckTimeout,
	}

	// Initialize both placement algorithms
	leastBusyAlgorithm := &placement.LeastBusyAlgorithm{}
	bestOfKAlgorithm := placement.NewBestOfK(getBestOfKConfig(ctx, featureFlags)).(*placement.BestOfK)

	o := Orchestrator{
		httpClient:           httpClient,
		analytics:            analyticsInstance,
		posthogClient:        posthogClient,
		nomadClient:          nomadClient,
		nodes:                smap.New[*nodemanager.Node](),
		leastBusyAlgorithm:   leastBusyAlgorithm,
		bestOfKAlgorithm:     bestOfKAlgorithm,
		featureFlagsClient:   featureFlags,
		accessTokenGenerator: accessTokenGenerator,
		routingCatalog:       routingCatalog,
		dbClient:             dbClient,
		sqlcDB:               sqlcDB,
		tel:                  tel,
		clusters:             clusters,

		sandboxCounter: sandboxCounter,
		createdCounter: createdCounter,
	}

	var sandboxStorage sandbox.Storage
	memoryStorage := memory.NewStorage()

	if redisClient != nil {
		redisStorage := redisbackend.NewStorage(redisClient)
		sandboxStorage = populate_redis.NewStorage(memoryStorage, redisStorage)
	} else {
		sandboxStorage = memoryStorage
	}

	reservationStorage := reservations.NewReservationStorage()

	o.sandboxStore = sandbox.NewStore(
		sandboxStorage,
		reservationStorage,
		[]sandbox.InsertCallback{
			o.addToNode,
		},
		[]sandbox.InsertCallback{
			o.observeTeamSandbox,
			o.countersInsert,
			o.analyticsInsert,
		},
	)

	// Evict old sandboxes
	sandboxEvictor := evictor.New(o.sandboxStore, o.RemoveSandbox)
	go sandboxEvictor.Start(ctx)

	teamMetricsObserver, err := metrics.NewTeamObserver(ctx, o.sandboxStore)
	if err != nil {
		zap.L().Error("Failed to create team metrics observer", zap.Error(err))

		return nil, fmt.Errorf("failed to create team metrics observer: %w", err)
	}

	o.teamMetricsObserver = teamMetricsObserver

	// For local development and testing, we skip the Nomad sync
	// Local cluster is used for single-node setups instead
	skipNomadSync := env.IsLocal()
	go o.keepInSync(ctx, o.sandboxStore, skipNomadSync)

	if err := o.setupMetrics(tel.MeterProvider); err != nil {
		zap.L().Error("Failed to setup metrics", zap.Error(err))

		return nil, fmt.Errorf("failed to setup metrics: %w", err)
	}

	go o.reportLongRunningSandboxes(ctx)
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
			zap.L().Info("Stopping status logging")

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

			zap.L().Info("API internal status",
				zap.Int("sandboxes_count", len(o.sandboxStore.Items(nil, []sandbox.State{sandbox.StateRunning}))),
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
		if err := node.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	zap.L().Info("Shutting down node clients", zap.Int("error_count", len(errs)), zap.Int("node_count", len(connectedNodes)))

	if o.metricsRegistration != nil {
		if err := o.metricsRegistration.Unregister(); err != nil {
			errs = append(errs, fmt.Errorf("failed to unregister metrics: %w", err))
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

	return errors.Join(errs...)
}

// getPlacementAlgorithm returns the appropriate placement algorithm based on the passed context
func (o *Orchestrator) getPlacementAlgorithm(ctx context.Context) placement.Algorithm {
	// Use sandbox ID as context key for feature flag evaluation
	useBestOfK, err := o.featureFlagsClient.BoolFlag(ctx, featureflags.BestOfKPlacementAlgorithm)
	if err != nil {
		zap.L().Error("Failed to evaluate placement algorithm feature flag, using least-busy",
			zap.Error(err))

		return o.leastBusyAlgorithm
	}

	if useBestOfK {
		return o.bestOfKAlgorithm
	}

	return o.leastBusyAlgorithm
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
			o.bestOfKAlgorithm.UpdateConfig(config)
		}
	}
}

func getBestOfKConfig(ctx context.Context, featureFlagsClient *featureflags.Client) placement.BestOfKConfig {
	k, err := featureFlagsClient.IntFlag(ctx, featureflags.BestOfKSampleSize)
	if err != nil {
		zap.L().Error("Failed to get BestOfKSampleSize flag", zap.Error(err))
		k = 3 // fallback to default
	}

	maxOvercommitPercent, err := featureFlagsClient.IntFlag(ctx, featureflags.BestOfKMaxOvercommit)
	if err != nil {
		zap.L().Error("Failed to get BestOfKMaxOvercommit flag", zap.Error(err))
	}

	alphaPercent, err := featureFlagsClient.IntFlag(ctx, featureflags.BestOfKAlpha)
	if err != nil {
		zap.L().Error("Failed to get BestOfKAlpha flag", zap.Error(err))
	}

	canFit, err := featureFlagsClient.BoolFlag(ctx, featureflags.BestOfKCanFit)
	if err != nil {
		zap.L().Error("Failed to get BestOfKCanFit flag", zap.Error(err))
	}

	tooManyStarting, err := featureFlagsClient.BoolFlag(ctx, featureflags.BestOfKTooManyStarting)
	if err != nil {
		zap.L().Error("Failed to get BestOfKTooManyStarting flag", zap.Error(err))
	}

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
