package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"

	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/dns"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type Orchestrator struct {
	nomadClient   *nomadapi.Client
	instanceCache *instance.InstanceCache
	nodes         map[string]*Node
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

	dnsServer := dns.New()
	go func() {
		fmt.Printf("Starting DNS server\n")

		dnsErr := dnsServer.Start("127.0.0.4", 53)
		if dnsErr != nil {
			log.Fatalf("Failed running DNS server: %v\n", dnsErr)
		}
	}()

	o := Orchestrator{
		analytics:   analyticsInstance,
		nomadClient: nomadClient,
		logger:      logger,
		tracer:      tracer,
		nodes:       make(map[string]*Node),
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
		go o.keepInSync(ctx, cache)
	}

	return &o, nil
}

func (o *Orchestrator) Close() error {
	var err error
	for _, node := range o.nodes {
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
