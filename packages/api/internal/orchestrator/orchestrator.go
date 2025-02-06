package orchestrator

import (
	"context"
	"errors"
	"os"

	"github.com/go-redis/redis/v8"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/dns"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Orchestrator struct {
	nomadClient   *nomadapi.Client
	instanceCache *instance.InstanceCache
	nodes         *smap.Map[*Node]
	tracer        trace.Tracer
	logger        logging.Logger
	analytics     *analyticscollector.Analytics
	dns           *dns.DNS
}

func New(
	ctx context.Context,
	tracer trace.Tracer,
	nomadClient *nomadapi.Client,
	logger logging.Logger,
	posthogClient *analyticscollector.PosthogClient,
	redisClient *redis.Client,
) (*Orchestrator, error) {
	analyticsInstance, err := analyticscollector.NewAnalytics()
	if err != nil {
		logger.Error("Error initializing Analytics client", zap.Error(err))
	}

	dnsServer := dns.New(ctx, redisClient, logger)

	if env.IsLocal() {
		logger.Info("Running locally, skipping starting DNS server")
	} else {
		logger.Info("Starting DNS server")
		dnsServer.Start(ctx, "0.0.0.0", os.Getenv("DNS_PORT"))
	}

	o := Orchestrator{
		analytics:   analyticsInstance,
		nomadClient: nomadClient,
		logger:      logger,
		tracer:      tracer,
		nodes:       smap.New[*Node](),
		dns:         dnsServer,
	}

	cache := instance.NewCache(
		analyticsInstance.Client,
		logger,
		o.getInsertInstanceFunction(ctx, logger),
		o.getDeleteInstanceFunction(ctx, posthogClient, logger),
	)

	o.instanceCache = cache

	if env.IsLocal() {
		logger.Info("Skipping syncing sandboxes, running locally")
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

	o.logger.Infof("shutting down node clients: %d of %d nodes had errors", len(errs), len(nodes))

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
