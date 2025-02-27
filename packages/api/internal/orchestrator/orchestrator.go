package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

const (
	// cacheHookTimeout is the timeout for all requests inside cache insert/delete hooks
	cacheHookTimeout = 5 * time.Minute

	statusLogInterval = time.Second * 10
)

type Orchestrator struct {
	nomadClient   *nomadapi.Client
	instanceCache *instance.InstanceCache
	nodes         *smap.Map[*Node]
	tracer        trace.Tracer
	logger        *zap.SugaredLogger
	analytics     *analyticscollector.Analytics
	dns           *dns.DNS
	dbClient      *db.DB
}

func New(
	ctx context.Context,
	tracer trace.Tracer,
	nomadClient *nomadapi.Client,
	logger *zap.Logger,
	posthogClient *analyticscollector.PosthogClient,
	redisClient *redis.Client,
	dbClient *db.DB,
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

	slogger := logger.Sugar()

	o := Orchestrator{
		analytics:   analyticsInstance,
		nomadClient: nomadClient,
		logger:      slogger,
		tracer:      tracer,
		nodes:       smap.New[*Node](),
		dns:         dnsServer,
		dbClient:    dbClient,
	}

	cache := instance.NewCache(
		ctx,
		analyticsInstance.Client,
		slogger,
		o.getInsertInstanceFunction(ctx, slogger, cacheHookTimeout),
		o.getDeleteInstanceFunction(ctx, posthogClient, slogger, cacheHookTimeout),
	)

	o.instanceCache = cache

	if env.IsLocal() {
		logger.Info("Skipping syncing sandboxes, running locally")
	} else {
		go o.keepInSync(cache)
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
			o.logger.Infof("Stopping status logging")
			return
		case <-ticker.C:
			var logMessage strings.Builder

			logMessage.WriteString("Orchestrator status:\n")
			logMessage.WriteString(fmt.Sprintf("  Sandboxes cache size: %d\n", o.instanceCache.Len()))
			logMessage.WriteString(fmt.Sprintf("  Nodes cache size: %d\n", o.nodes.Count()))

			for _, nodeItem := range o.nodes.Items() {
				logMessage.WriteString(fmt.Sprintf("    Node %s: %d sandboxes in progress, %d CPU usage\n",
					nodeItem.Info.ID, len(nodeItem.sbxsInProgress.Items()), nodeItem.CPUUsage.Load()))
			}
			logMessage.WriteString("    All Nodes listed\n")

			o.logger.Infof(logMessage.String())
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

// WaitForPause waits for the instance to be paused and returns the node info where the instance was paused on.
func (o *Orchestrator) WaitForPause(ctx context.Context, sandboxID string) (*node.NodeInfo, error) {
	return o.instanceCache.WaitForPause(ctx, sandboxID)
}
