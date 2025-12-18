package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const nodeHealthCheckTimeout = time.Second * 2

func (o *Orchestrator) connectToClusterNode(ctx context.Context, cluster *clusters.Cluster, i *clusters.Instance) {
	orchestratorNode, err := nodemanager.NewNode(ctx, cluster.ID, cluster.SandboxDomain, i)
	if err != nil {
		logger.L().Error(ctx, "Failed to create node", zap.Error(err))

		return
	}

	o.registerNode(orchestratorNode)
}

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

func (o *Orchestrator) GetNode(clusterID uuid.UUID, nodeID string) *nodemanager.Node {
	scopedKey := o.scopedNodeID(clusterID, nodeID)
	n, _ := o.nodes.Get(scopedKey)

	return n
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
func (o *Orchestrator) GetNodeByIDOrNomadShortID(clusterID uuid.UUID, nodeIDOrNomadNodeShortID string) *nodemanager.Node {
	// First try to get by nomad short ID
	n := o.GetNodeByNomadShortID(nodeIDOrNomadNodeShortID)
	if n != nil {
		return n
	}

	// Fallback to use id
	return o.GetNode(clusterID, nodeIDOrNomadNodeShortID)
}

// Deprecated: use GetNode instead
func (o *Orchestrator) GetNodeByNomadShortID(id string) *nodemanager.Node {
	for _, n := range o.nodes.Items() {
		if n.IsLocal() && n.ID[:consts.NodeIDLength] == id {
			return n
		}
	}

	return nil
}

func (o *Orchestrator) NodeCount() int {
	return o.nodes.Count()
}
