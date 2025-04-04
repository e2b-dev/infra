package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/dns"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	// cacheHookTimeout is the timeout for all requests inside cache insert/delete hooks.
	cacheHookTimeout = 5 * time.Minute

	statusLogInterval = time.Second * 20
)

type Orchestrator struct {
	nomadClient   *nomadapi.Client
	instanceCache *instance.InstanceCache
	nodes         *smap.Map[*Node]
	tracer        trace.Tracer
	analytics     *analyticscollector.Analytics
	dns           *dns.DNS
	dbClient      *db.DB
}

func New(
	ctx context.Context,
	tracer trace.Tracer,
	nomadClient *nomadapi.Client,
	posthogClient *analyticscollector.PosthogClient,
	redisClient *redis.Client,
	redisClusterClient *redis.ClusterClient,
	dbClient *db.DB,
) (*Orchestrator, error) {
	analyticsInstance, err := analyticscollector.NewAnalytics()
	if err != nil {
		zap.L().Error("Error initializing Analytics client", zap.Error(err))
	}

	dnsServer := dns.New(ctx, redisClient, redisClusterClient)

	if env.IsLocal() {
		zap.L().Info("Running locally, skipping starting DNS server")
	} else {
		zap.L().Info("Starting DNS server")
		dnsServer.Start(ctx, "0.0.0.0", os.Getenv("DNS_PORT"))
	}

	o := Orchestrator{
		analytics:   analyticsInstance,
		nomadClient: nomadClient,
		tracer:      tracer,
		nodes:       smap.New[*Node](),
		dns:         dnsServer,
		dbClient:    dbClient,
	}

	cache := instance.NewCache(
		ctx,
		analyticsInstance.Client,
		o.getInsertInstanceFunction(ctx, cacheHookTimeout),
		o.getDeleteInstanceFunction(ctx, posthogClient, cacheHookTimeout),
	)

	o.instanceCache = cache

	if env.IsLocal() {
		zap.L().Info("Skipping syncing sandboxes, running locally")
		// Add a local node for local development, if there isn't any, it fails silently
		err := o.connectToNode(ctx, &node.NodeInfo{
			ID:                  "testclient",
			OrchestratorAddress: fmt.Sprintf("%s:%s", "127.0.0.1", consts.OrchestratorPort),
			IPAddress:           "127.0.0.1",
		})

		if err != nil {
			zap.L().Error("Error connecting to local node. If you're starting the API server locally, make sure you run 'make connect-orchestrator' to connect to the node remotely before starting the local API server.", zap.Error(err))
			return nil, err
		}
	} else {
		go o.keepInSync(ctx, cache)
		go o.startNodeAnalytics(ctx)
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
			nodes := make([]map[string]interface{}, 0, o.nodes.Count())

			for _, nodeItem := range o.nodes.Items() {
				if nodeItem == nil {
					nodes = append(nodes, map[string]interface{}{
						"id": "nil",
					})
				} else {
					nodes = append(nodes, map[string]interface{}{
						"id":                    nodeItem.Info.ID,
						"status":                nodeItem.Status(),
						"socket_status":         nodeItem.Client.connection.GetState().String(),
						"in_progress_count":     nodeItem.sbxsInProgress.Count(),
						"failed_to_start_count": nodeItem.createFails.Load(),
					})
				}
			}

			zap.L().Info("API internal status",
				zap.Int("sandboxes_count", o.instanceCache.Len()),
				zap.Int("nodes_count", o.nodes.Count()),
				zap.Any("nodes", nodes),
			)
		}
	}
}

func (o *Orchestrator) Close(ctx context.Context) error {
	var errs []error

	nodes := o.nodes.Items()
	for _, node := range nodes {
		if err := node.Client.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	zap.L().Info("shutting down node clients", zap.Int("error_count", len(errs)), zap.Int("node_count", len(nodes)))

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
func (o *Orchestrator) WaitForPause(ctx context.Context, sandboxID string) (*node.NodeInfo, error) {
	return o.instanceCache.WaitForPause(ctx, sandboxID)
}
