package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator")

// cacheSyncTime is the time to sync the cache with the actual instances in Orchestrator.
const cacheSyncTime = 20 * time.Second

func (o *Orchestrator) GetSandbox(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	return o.sandboxStore.Get(ctx, sandboxID)
}

// keepInSync the cache with the actual instances in Orchestrator to handle instances that died.
func (o *Orchestrator) keepInSync(ctx context.Context, store *sandbox.Store) {
	// Run the first sync immediately
	logger.L().Info(ctx, "Running the initial node sync")
	o.syncNodes(ctx, store)

	// Sync the nodes every cacheSyncTime
	ticker := time.NewTicker(cacheSyncTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.L().Info(ctx, "Stopping keepInSync")

			return
		case <-ticker.C:
			o.syncNodes(ctx, store)
		}
	}
}

func (o *Orchestrator) syncNodes(ctx context.Context, store *sandbox.Store) {
	ctxTimeout, cancel := context.WithTimeout(ctx, cacheSyncTime)
	defer cancel()

	spanCtx, span := tracer.Start(ctxTimeout, "keep-in-sync")
	defer span.End()

	var wg sync.WaitGroup

	// Wait for nodes discovery to finish
	o.syncDiscoveredNodes(spanCtx)

	// Sync state of all nodes currently in the pool
	syncNodesSpanCtx, syncNodesSpan := tracer.Start(spanCtx, "keep-in-sync-existing")
	defer syncNodesSpan.End()

	defer wg.Wait()
	for _, n := range o.nodes.Items() {
		wg.Go(func() {
			err := o.syncNode(syncNodesSpanCtx, n, store)
			if err != nil {
				logger.L().Error(syncNodesSpanCtx, "Error syncing node", zap.Error(err))
				err = n.Close(context.WithoutCancel(syncNodesSpanCtx))
				if err != nil {
					logger.L().Error(syncNodesSpanCtx, "Error closing node", zap.Error(err))
				}

				o.deregisterNode(n)
			}
		})
	}
}

func (o *Orchestrator) syncDiscoveredNodes(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()

	_, connectClusteredSpan := tracer.Start(ctx, "keep-in-sync-connect-clustered-nodes")
	defer connectClusteredSpan.End()

	// Connect clustered nodes that are not in the list, yet
	// We need to iterate over all clusters and their nodes
	for _, cluster := range o.clusters.GetClusters() {
		for _, n := range cluster.GetOrchestrators() {
			// If the node is not in the list, connect to it
			if o.GetNode(cluster.ID, n.NodeID) == nil {
				wg.Go(func() {
					o.connectToClusterNode(ctx, cluster, n)
				})
			}
		}
	}
}

func (o *Orchestrator) syncNode(ctx context.Context, node *nodemanager.Node, store *sandbox.Store) error {
	ctx, childSpan := tracer.Start(ctx, "sync-cluster-node")
	telemetry.SetAttributes(ctx, telemetry.WithNodeID(node.ID), telemetry.WithClusterID(node.ClusterID))
	defer childSpan.End()

	cluster, clusterFound := o.clusters.GetClusterById(node.ClusterID)
	if !clusterFound {
		return fmt.Errorf("cluster not found")
	}

	// We want to find not just node, but explicitly node with expected service instance ID
	// because node can stay same and instance id will change after node restart, witch should trigger node de-registration from pool.
	instanceID := node.Metadata().ServiceInstanceID
	_, found := cluster.GetByServiceInstanceID(instanceID)
	if !found {
		return fmt.Errorf("node instance not found with instance ID '%s'", instanceID)
	}

	// Unified call for syncing node state across different node types
	node.Sync(ctx, store)

	return nil
}
