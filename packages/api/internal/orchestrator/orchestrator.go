package orchestrator

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/dns"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// cacheHookTimeout is the timeout for all requests inside cache insert/delete hooks
const cacheHookTimeout = 5 * time.Minute

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
	dbClient *db.DB,
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
	} else {
		go o.keepInSync(cache)
	}

	return &o, nil
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
