package orchestrator

import (
	"context"
	"errors"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/go-redis/redis/v8"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/dns"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Orchestrator struct {
	nomadClient   *nomadapi.Client
	instanceCache *instance.InstanceCache
	nodes         *smap.Map[*Node]
	tracer        trace.Tracer
	logger        *zap.SugaredLogger
	analytics     *analyticscollector.Analytics
	dns           *dns.DNS
}

func New(
	ctx context.Context,
	tracer trace.Tracer,
	nomadClient *nomadapi.Client,
	logger *zap.SugaredLogger,
	posthogClient *analyticscollector.PosthogClient,
	redisClient *redis.Client,
) (*Orchestrator, error) {
	analyticsInstance, err := analyticscollector.NewAnalytics()
	if err != nil {
		logger.Error("Error initializing Analytics client", zap.Error(err))
	}

	dnsServer := dns.New(redisClient)
	port := utils.RequiredEnv("DNS_PORT", "Local DNS server resolving IPs for sandboxes")

	if env.IsLocal() {
		logger.Info("Running locally, skipping starting DNS server")
	} else {
		go func() {
			logger.Info("Starting DNS server")

			if err := dnsServer.Start(ctx, "0.0.0.0", port); err != nil {
				logger.Panic("Failed starting DNS server", zap.Error(err))
			}
		}()
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

func (o *Orchestrator) Close() error {
	var err error
	for _, node := range o.nodes.Items() {
		closeErr := node.Client.Close()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}

	closeErr := o.analytics.Close()
	if closeErr != nil {
		err = errors.Join(err, closeErr)
	}

	return err
}
