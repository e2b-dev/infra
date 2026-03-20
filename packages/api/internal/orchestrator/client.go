package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const nodeHealthCheckTimeout = time.Second * 2

func (o *Orchestrator) connectToNode(ctx context.Context, discovered nodemanager.NomadServiceDiscovery) error {
	ctx, childSpan := tracer.Start(ctx, "connect-to-node")
	defer childSpan.End()

	// connectGroup is keyed by NomadNodeShortID so that any concurrent caller
	// (background sync goroutine or on-demand getOrConnectNode) that races to
	// connect the same Nomad node shares a single dial attempt. The second
	// caller simply waits and gets the same nil result; registerNode is never
	// called twice for the same node.
	_, err, _ := o.connectGroup.Do(discovered.NomadNodeShortID, func() (any, error) {
		// Re-check inside the singleflight. The caller verified absence before
		// calling us, but a previous completed Do for this key (or a concurrent
		// path that bypasses the singleflight) may have already registered the
		// node in the meantime. Avoid a redundant dial.
		if o.GetNodeByNomadShortID(discovered.NomadNodeShortID) != nil {
			return nil, nil
		}

		orchestratorNode, err := nodemanager.New(ctx, o.tel.TracerProvider, o.tel.MeterProvider, discovered)
		if err != nil {
			return nil, err
		}

		o.registerNode(orchestratorNode)

		return nil, nil
	})

	return err
}

func (o *Orchestrator) connectToClusterNode(ctx context.Context, cluster *clusters.Cluster, i *clusters.Instance) {
	// connectGroup is keyed by scopedNodeID so that concurrent callers targeting
	// the same cluster instance share a single dial attempt.
	scopedKey := o.scopedNodeID(cluster.ID, i.NodeID)

	o.connectGroup.Do(scopedKey, func() (any, error) { //nolint:errcheck
		// Re-check inside the singleflight for the same reason as connectToNode.
		if o.GetNode(cluster.ID, i.NodeID) != nil {
			return nil, nil
		}

		orchestratorNode, err := nodemanager.NewClusterNode(ctx, i.GetClient(), cluster.ID, cluster.SandboxDomain, i)
		if err != nil {
			logger.L().Error(ctx, "Failed to create node", zap.Error(err))

			return nil, nil
		}

		o.registerNode(orchestratorNode)

		return nil, nil
	})
}

// registerNode adds the given node to the in-memory map of nodes
// It has to be called only once per node
func (o *Orchestrator) registerNode(node *nodemanager.Node) {
	scopedKey := o.scopedNodeID(node.ClusterID, node.ID)
	o.nodes.Insert(scopedKey, node)
}

func (o *Orchestrator) deregisterNode(node *nodemanager.Node) {
	scopedKey := o.scopedNodeID(node.ClusterID, node.ID)
	o.nodes.Remove(scopedKey)
}

// When prefixed with cluster ID, node is unique in the map containing nodes from multiple clusters
func (o *Orchestrator) scopedNodeID(clusterID uuid.UUID, nodeID string) string {
	if clusterID == consts.LocalClusterID {
		return nodeID
	}

	return fmt.Sprintf("%s-%s", clusterID.String(), nodeID)
}

func (o *Orchestrator) listNomadNodes(ctx context.Context) ([]nodemanager.NomadServiceDiscovery, error) {
	_, listSpan := tracer.Start(ctx, "list-nomad-nodes")
	defer listSpan.End()

	options := &nomadapi.QueryOptions{
		// TODO: Use variable for node pool name ("default")
		Filter: "Status == \"ready\" and NodePool == \"default\"",
	}
	nomadNodes, _, err := o.nomadClient.Nodes().List(options.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	result := make([]nodemanager.NomadServiceDiscovery, 0, len(nomadNodes))
	for _, n := range nomadNodes {
		result = append(result, nodemanager.NomadServiceDiscovery{
			NomadNodeShortID:    n.ID[:consts.NodeIDLength],
			OrchestratorAddress: fmt.Sprintf("%s:%d", n.Address, consts.OrchestratorAPIPort),
			IPAddress:           n.Address,
		})
	}

	return result, nil
}

func (o *Orchestrator) GetNode(clusterID uuid.UUID, nodeID string) *nodemanager.Node {
	scopedKey := o.scopedNodeID(clusterID, nodeID)
	n, _ := o.nodes.Get(scopedKey)

	return n
}

// getOrConnectNode returns a node from the in-memory cache. When the node is absent it
// performs a targeted on-demand discovery and connection attempt, handling the race
// condition where a new orchestrator joined the cluster after this API instance's last
// sync cycle but another API instance already routed a sandbox there.
//
// There are two distinct gaps that must be covered:
//
//   - Gap 1 (0–5 s for clusters, 0–20 s for Nomad): the node exists in the upstream
//     source (Nomad / remote service discovery) but has not yet been pulled into the
//     local instance map by the background sync loop.
//   - Gap 2 (0–20 s): the node is in the local instance map but has not yet been
//     promoted into o.nodes by keepInSync.
//
// For Nomad-managed nodes both gaps are closed by querying Nomad directly and
// connecting any nodes not yet in the pool. The orchestrator's self-reported node ID
// (stored in the sandbox record) cannot be mapped back to a specific Nomad node
// without first connecting, so all unknown Nomad nodes are opportunistically connected.
//
// For cluster nodes Gap 1 is closed by calling SyncInstances, which queries the
// remote service discovery and populates cluster.instances. Gap 2 is then closed by
// iterating the freshly-updated instance map and calling connectToClusterNode.
//
// discoveryGroup ensures that concurrent requests targeting the same missing
// node share a single discovery attempt rather than fanning out.
func (o *Orchestrator) getOrConnectNode(ctx context.Context, clusterID uuid.UUID, nodeID string) *nodemanager.Node {
	if node := o.GetNode(clusterID, nodeID); node != nil {
		return node
	}

	logger.L().Warn(ctx, "Node not found in cache, attempting on-demand connection",
		logger.WithNodeID(nodeID),
		zap.String("cluster_id", clusterID.String()),
	)

	scopedKey := o.scopedNodeID(clusterID, nodeID)

	o.discoveryGroup.Do(scopedKey, func() (any, error) { //nolint:errcheck
		connectCtx, cancel := context.WithTimeout(ctx, nodeConnectTimeout)
		defer cancel()

		if clusterID == consts.LocalClusterID {
			// Nomad-managed node: list all ready Nomad nodes and connect any that are
			// not yet in the pool. Once the new node is connected its orchestrator ID
			// becomes the map key, making the subsequent GetNode call succeed.
			nomadNodes, err := o.listNomadNodes(connectCtx)
			if err != nil {
				logger.L().Error(connectCtx, "Error listing Nomad nodes during on-demand connect",
					zap.Error(err), logger.WithNodeID(nodeID))

				return nil, nil
			}

			for _, n := range nomadNodes {
				if o.GetNodeByNomadShortID(n.NomadNodeShortID) == nil {
					if err := o.connectToNode(connectCtx, n); err != nil {
						logger.L().Error(connectCtx, "Error connecting to Nomad node on demand",
							zap.Error(err), zap.String("nomad_short_id", n.NomadNodeShortID))
					}
				}
			}
		} else {
			// Cluster node: first force a fresh service discovery query so that nodes
			// which joined after the last periodic sync are pulled into cluster.instances
			// Then load them into orchestrator nodes map.
			if cluster, found := o.clusters.GetClusterById(clusterID); found {
				if err := cluster.SyncInstances(connectCtx); err != nil {
					logger.L().Error(connectCtx, "Error syncing cluster instances during on-demand connect",
						zap.Error(err), logger.WithNodeID(nodeID))
				}

				for _, instance := range cluster.GetOrchestrators() {
					if instance.NodeID == nodeID {
						o.connectToClusterNode(connectCtx, cluster, instance)

						break
					}
				}
			}
		}

		return nil, nil
	})

	return o.GetNode(clusterID, nodeID)
}

func (o *Orchestrator) GetClusterNodes(clusterID uuid.UUID) []*nodemanager.Node {
	clusterNodes := make([]*nodemanager.Node, 0)
	for _, n := range o.nodes.Items() {
		if n.ClusterID == clusterID {
			clusterNodes = append(clusterNodes, n)
		}
	}

	return clusterNodes
}

// Deprecated: use GetNode instead
func (o *Orchestrator) GetNodeByNomadShortID(id string) *nodemanager.Node {
	for _, n := range o.nodes.Items() {
		if n.NomadNodeShortID == id {
			return n
		}
	}

	return nil
}

func (o *Orchestrator) NodeCount() int {
	return o.nodes.Count()
}
