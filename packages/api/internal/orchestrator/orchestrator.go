package orchestrator

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/dns"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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
	nodes                   *smap.Map[*Node]
	tracer                  trace.Tracer
	analytics               *analyticscollector.Analytics
	dns                     *dns.DNS
	dbClient                *db.DB
	tel                     *telemetry.Client
	clusters                *edge.Pool
	metricsRegistration     metric.Registration
	createdSandboxesCounter metric.Int64Counter
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
		httpClient:  httpClient,
		analytics:   analyticsInstance,
		nomadClient: nomadClient,
		tracer:      tracer,
		nodes:       smap.New[*Node](),
		dns:         dnsServer,
		dbClient:    dbClient,
		tel:         tel,
		clusters:    clusters,
	}

	cache := instance.NewCache(
		ctx,
		tel.MeterProvider,
		o.getInsertInstanceFunction(ctx, cacheHookTimeout),
		o.getDeleteInstanceFunction(ctx, posthogClient, cacheHookTimeout),
	)

	o.instanceCache = cache

	if env.IsLocal() {
		zap.L().Info("Skipping syncing sandboxes, running locally")
		// Add a local node for local development, if there isn't any, it fails silently
		err := o.connectToNode(ctx, nomadServiceDiscovery{
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
			nodes := make([]map[string]interface{}, 0, o.nodes.Count())

			for _, nodeItem := range o.nodes.Items() {
				if nodeItem == nil {
					nodes = append(nodes, map[string]interface{}{
						"id": "nil",
					})
				} else {
					nodes = append(nodes, map[string]interface{}{
						"id":                nodeItem.Info.NodeID,
						"status":            nodeItem.Status(),
						"socket_status":     nodeItem.client.Connection.GetState().String(),
						"in_progress_count": nodeItem.sbxsInProgress.Count(),
						"sbx_start_success": nodeItem.createSuccess.Load(),
						"sbx_start_fail":    nodeItem.createFails.Load(),
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
		if err := node.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	zap.L().Info("Shutting down node clients", zap.Int("error_count", len(errs)), zap.Int("node_count", len(nodes)))

	if o.metricsRegistration != nil {
		if err := o.metricsRegistration.Unregister(); err != nil {
			errs = append(errs, fmt.Errorf("failed to unregister metrics: %w", err))
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
func (o *Orchestrator) WaitForPause(ctx context.Context, sandboxID string) (*node.NodeInfo, error) {
	return o.instanceCache.WaitForPause(ctx, sandboxID)
}

func (o *Orchestrator) AdminNodes() []*api.Node {
	nodes := make(map[string]*api.Node)

	for _, n := range o.nodes.Items() {
		// Skip all nodes that are not running in local (Nomad) cluster
		if n.Info.ClusterID != uuid.Nil {
			continue
		}

		meta := n.metadata()
		nodes[n.Info.NomadNodeShortID] = &api.Node{
			NodeID:               n.Info.NomadNodeShortID,
			ClusterID:            n.Info.ClusterID.String(),
			Status:               n.Status(),
			CreateSuccesses:      n.createSuccess.Load(),
			CreateFails:          n.createFails.Load(),
			SandboxStartingCount: n.sbxsInProgress.Count(),
			Version:              meta.version,
			Commit:               meta.commit,
		}
	}

	for _, sbx := range o.instanceCache.Items() {
		n, ok := nodes[sbx.Node.NomadNodeShortID]
		if !ok {
			zap.L().Error("node for sandbox wasn't found", logger.WithNodeID(sbx.Node.NodeID), logger.WithClusterID(sbx.Node.ClusterID), logger.WithSandboxID(sbx.Instance.SandboxID))
			continue
		}

		n.AllocatedCPU += int32(sbx.VCpu)
		n.AllocatedMemoryMiB += int32(sbx.RamMB)
		n.SandboxCount += 1
	}

	var result []*api.Node
	for _, n := range nodes {
		result = append(result, n)
	}

	slices.SortFunc(result, func(i, j *api.Node) int {
		return cmp.Compare(i.NodeID, j.NodeID)
	})

	return result
}

func (o *Orchestrator) AdminNodeDetail(nomadNodeShortID string) (*api.NodeDetail, error) {
	n := o.GetNodeByNomadShortID(nomadNodeShortID)
	if n == nil {
		return nil, ErrNodeNotFound
	}

	builds := n.buildCache.Keys()
	meta := n.metadata()
	node := &api.NodeDetail{
		NodeID:    n.Info.NomadNodeShortID,
		ClusterID: n.Info.ClusterID.String(),

		Status:          n.Status(),
		CachedBuilds:    builds,
		CreateSuccesses: n.createSuccess.Load(),
		CreateFails:     n.createFails.Load(),
		Version:         meta.version,
		Commit:          meta.commit,
	}

	for _, sbx := range o.instanceCache.Items() {
		if sbx.Node.NodeID == n.Info.NodeID && sbx.Node.ClusterID == n.Info.ClusterID {
			var metadata *api.SandboxMetadata
			if sbx.Metadata != nil {
				meta := api.SandboxMetadata(sbx.Metadata)
				metadata = &meta
			}

			node.Sandboxes = append(node.Sandboxes, api.ListedSandbox{
				Alias:      sbx.Instance.Alias,
				ClientID:   consts.ClientID,
				CpuCount:   api.CPUCount(sbx.VCpu),
				MemoryMB:   api.MemoryMB(sbx.RamMB),
				DiskSizeMB: api.DiskSizeMB(sbx.TotalDiskSizeMB),
				EndAt:      sbx.GetEndTime(),
				Metadata:   metadata,
				SandboxID:  sbx.Instance.SandboxID,
				StartedAt:  sbx.StartTime,
				TemplateID: sbx.Instance.TemplateID,
			})
		}
	}

	return node, nil
}
