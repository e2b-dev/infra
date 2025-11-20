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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator")

// cacheSyncTime is the time to sync the cache with the actual instances in Orchestrator.
const cacheSyncTime = 20 * time.Second

func (o *Orchestrator) GetSandbox(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	return o.sandboxStore.Get(ctx, sandboxID)
}

// keepInSync the cache with the actual instances in Orchestrator to handle instances that died.
func (o *Orchestrator) keepInSync(ctx context.Context, store *sandbox.Store, skipSyncingWithNomad bool) {
	// Run the first sync immediately
	zap.L().Info("Running the initial node sync")
	o.syncNodes(ctx, store, skipSyncingWithNomad)

	// Sync the nodes every cacheSyncTime
	ticker := time.NewTicker(cacheSyncTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			zap.L().Info("Stopping keepInSync")

			return
		case <-ticker.C:
			o.syncNodes(ctx, store, skipSyncingWithNomad)
		}
	}
}

func (o *Orchestrator) syncNodes(ctx context.Context, store *sandbox.Store, skipSyncingWithNomad bool) {
	ctxTimeout, cancel := context.WithTimeout(ctx, cacheSyncTime)
	defer cancel()

	spanCtx, span := tracer.Start(ctxTimeout, "keep-in-sync")
	defer span.End()

	var wg sync.WaitGroup

	nomadNodes := make([]nodemanager.NomadServiceDiscovery, 0)

	// Optionally, skip syncing from Nomad service discovery
	if !skipSyncingWithNomad {
		nomadSD, err := o.listNomadNodes(spanCtx)
		if err != nil {
			zap.L().Error("Error listing orchestrator nodes", zap.Error(err))

			return
		}

		nomadNodes = nomadSD
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		o.syncLocalDiscoveredNodes(spanCtx, nomadNodes)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		o.syncClusterDiscoveredNodes(spanCtx)
	}()

	// Wait for nodes discovery to finish
	wg.Wait()

	// Sync state of all nodes currently in the pool
	syncNodesSpanCtx, syncNodesSpan := tracer.Start(spanCtx, "keep-in-sync-existing")
	defer syncNodesSpan.End()

	defer wg.Wait()
	for _, n := range o.nodes.Items() {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// cluster and local nodes needs to by synced differently,
			// because each of them is taken from different source pool
			var err error
			if n.IsNomadManaged() {
				err = o.syncNode(syncNodesSpanCtx, n, nomadNodes, store)
			} else {
				err = o.syncClusterNode(syncNodesSpanCtx, n, store)
			}
			if err != nil {
				zap.L().Error("Error syncing node", zap.Error(err))
				err = n.Close()
				if err != nil {
					zap.L().Error("Error closing grpc connection", zap.Error(err))
				}

				o.deregisterNode(n)
			}
		}()
	}
}

func (o *Orchestrator) syncLocalDiscoveredNodes(ctx context.Context, discovered []nodemanager.NomadServiceDiscovery) {
	// Connect local nodes that are not in the list, yet
	connectLocalSpanCtx, connectLocalSpan := tracer.Start(ctx, "keep-in-sync-connect-local-nodes")
	defer connectLocalSpan.End()

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, n := range discovered {
		// If the node is not in the list, connect to it
		if o.GetNodeByNomadShortID(n.NomadNodeShortID) == nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := o.connectToNode(connectLocalSpanCtx, n)
				if err != nil {
					zap.L().Error("Error connecting to node", zap.Error(err))
				}
			}()
		}
	}
}

func (o *Orchestrator) syncClusterDiscoveredNodes(ctx context.Context) {
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
				wg.Add(1)
				go func() {
					defer wg.Done()
					o.connectToClusterNode(ctx, cluster, n)
				}()
			}
		}
	}
}

func (o *Orchestrator) syncClusterNode(ctx context.Context, node *nodemanager.Node, store *sandbox.Store) error {
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

func (o *Orchestrator) syncNode(ctx context.Context, node *nodemanager.Node, discovered []nodemanager.NomadServiceDiscovery, store *sandbox.Store) error {
	ctx, childSpan := tracer.Start(ctx, "sync-node")
	telemetry.SetAttributes(ctx, telemetry.WithNodeID(node.ID))
	defer childSpan.End()

	found := false
	for _, activeNode := range discovered {
		if node.NomadNodeShortID == activeNode.NomadNodeShortID {
			found = true

			break
		}
	}

	if !found {
		return fmt.Errorf("node '%s' not found in the discovered nodes", node.NomadNodeShortID)
	}

	// Unified call for syncing node state across different node types
	node.Sync(ctx, store)

	return nil
}
