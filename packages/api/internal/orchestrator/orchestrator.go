package orchestrator

import (
	"context"
	"errors"
	"log"

	redis "github.com/go-redis/redis/v8"
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
) (*Orchestrator, error) {
	analyticsInstance, err := analyticscollector.NewAnalytics()
	if err != nil {
		logger.Errorf("Error initializing Analytics client\n: %v", err)
	}

	o := Orchestrator{
		analytics:   analyticsInstance,
		nomadClient: nomadClient,
		logger:      logger,
		tracer:      tracer,
		nodes:       smap.New[*Node](),
	}

	cache := instance.NewCache(
		analyticsInstance.Client,
		logger,
		o.getInsertInstanceFunction(ctx, logger),
		o.getDeleteInstanceFunction(ctx, posthogClient, logger),
	)

	o.instanceCache = cache

	rdbOpts := &redis.Options{Addr: "127.0.0.1:6379"}

	fallbackResolverFn := func(sandboxID string) (string, bool) {
		for _, apiNode := range o.GetNodes() {
			if detail := o.GetNodeDetail(apiNode.NodeID); detail != nil {
				for _, sb := range detail.Sandboxes {
					if sandboxID == sb.SandboxID {
						if node := o.GetNode(apiNode.NodeID); node != nil {
							return node.Info.IPAddress, true
						}
					}
				}
			}
		}
		return "", false
	}

	o.dns = dns.New(ctx, rdbOpts, fallbackResolverFn, logger)

	if env.IsLocal() {
		logger.Info("Running locally, skipping starting DNS server")
		logger.Info("Running locally, skipping syncing sandboxes")
	} else {
		go func() {
			logger.Info("Starting DNS server")

			if err := o.dns.Start("127.0.0.4", 53); err != nil {
				log.Fatalf("Failed running DNS server: %v\n", err)
			}
		}()

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
