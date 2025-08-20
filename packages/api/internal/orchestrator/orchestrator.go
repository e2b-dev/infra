package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/dns"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	"github.com/e2b-dev/infra/packages/api/internal/metrics"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodes"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// cacheHookTimeout is the timeout for all requests inside cache insert/delete hooks.
	cacheHookTimeout = 5 * time.Minute

	statusLogInterval = time.Second * 20
)

var ErrNodeNotFound = errors.New("node not found")

type Orchestrator struct {
	httpClient              *http.Client
	nomadClient             *nomadapi.Client
	instanceCache           *instance.InstanceCache
	nodes                   *smap.Map[*nodes.Node]
	placementAlgorithm      placement.Algorithm
	tracer                  trace.Tracer
	analytics               *analyticscollector.Analytics
	dns                     *dns.DNS
	dbClient                *db.DB
	tel                     *telemetry.Client
	clusters                *edge.Pool
	metricsRegistration     metric.Registration
	createdSandboxesCounter metric.Int64Counter
	teamMetricsObserver     *metrics.TeamObserver
}

func New(
	ctx context.Context,
	tel *telemetry.Client,
	tracer trace.Tracer,
	nomadClient *nomadapi.Client,
	posthogClient *analyticscollector.PosthogClient,
	redisClient redis.UniversalClient,
	dbClient *db.DB,
	clusters *edge.Pool,
) (*Orchestrator, error) {
	analyticsInstance, err := analyticscollector.NewAnalytics()
	if err != nil {
		zap.L().Error("Error initializing Analytics client", zap.Error(err))
	}

	dnsServer := dns.New(ctx, redisClient)

	if env.IsLocal() {
		zap.L().Info("Running locally, skipping starting DNS server")
	} else {
		zap.L().Info("Starting DNS server")
		dnsServer.Start(ctx, "0.0.0.0", os.Getenv("DNS_PORT"))
	}

	httpClient := &http.Client{
		Timeout: nodeHealthCheckTimeout,
	}

	o := Orchestrator{
		httpClient:         httpClient,
		analytics:          analyticsInstance,
		nomadClient:        nomadClient,
		tracer:             tracer,
		nodes:              smap.New[*nodes.Node](),
		placementAlgorithm: &placement.LeastBusyAlgorithm{},
		dns:                dnsServer,
		dbClient:           dbClient,
		tel:                tel,
		clusters:           clusters,
	}

	cache := instance.NewCache(
		ctx,
		tel.MeterProvider,
		o.getInsertInstanceFunction(ctx, cacheHookTimeout),
		o.getDeleteInstanceFunction(ctx, posthogClient, cacheHookTimeout),
	)

	o.instanceCache = cache

	teamMetricsObserver, err := metrics.NewTeamObserver(ctx, cache)
	if err != nil {
		zap.L().Error("Failed to create team metrics observer", zap.Error(err))
		return nil, fmt.Errorf("failed to create team metrics observer: %w", err)
	}

	o.teamMetricsObserver = teamMetricsObserver

	if env.IsLocal() {
		zap.L().Info("Skipping syncing sandboxes, running locally")
		// Add a local node for local development, if there isn't any, it fails silently
		err := o.connectToNode(ctx, nodes.NomadServiceDiscovery{
			NomadNodeShortID:    "testclient",
			OrchestratorAddress: fmt.Sprintf("%s:%s", "127.0.0.1", consts.OrchestratorPort),
			IPAddress:           "127.0.0.1",
		})
		if err != nil {
			zap.L().Error("Error connecting to local node. If you're starting the API server locally, make sure you run 'make connect-orchestrator' to connect to the node remotely before starting the local API server.", zap.Error(err))
			return nil, err
		}
	} else {
		go o.keepInSync(ctx, cache)
		go o.reportLongRunningSandboxes(ctx)
	}

	if err := o.setupMetrics(tel.MeterProvider); err != nil {
		zap.L().Error("Failed to setup metrics", zap.Error(err))
		return nil, fmt.Errorf("failed to setup metrics: %w", err)
	}

	go o.startStatusLogging(ctx)

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
			connectedNodes := make([]map[string]interface{}, 0, o.nodes.Count())

			for _, nodeItem := range o.nodes.Items() {
				if nodeItem == nil {
					connectedNodes = append(connectedNodes, map[string]interface{}{
						"id": "nil",
					})
				} else {
					connectedNodes = append(connectedNodes, map[string]interface{}{
						"id":        nodeItem.ID,
						"status":    nodeItem.Status(),
						"sandboxes": nodeItem.Metrics().SandboxCount,
					})
				}
			}

			zap.L().Info("API internal status",
				zap.Int("sandboxes_count", o.instanceCache.Len()),
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

	if o.dns != nil {
		if err := o.dns.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// WaitForPause waits for the instance to be paused and returns the node info where the instance was paused on.
func (o *Orchestrator) WaitForPause(ctx context.Context, sandboxID string) (nodeID string, err error) {
	return o.instanceCache.WaitForPause(ctx, sandboxID)
}
