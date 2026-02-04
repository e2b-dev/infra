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

	orchestratorNode, err := nodemanager.New(ctx, o.tel.TracerProvider, o.tel.MeterProvider, discovered)
	if err != nil {
		return err
	}

	// Update host metrics from service info
	o.registerNode(orchestratorNode)

	return nil
}

func (o *Orchestrator) connectToClusterNode(ctx context.Context, cluster *clusters.Cluster, i *clusters.Instance) {
	orchestratorNode, err := nodemanager.NewClusterNode(ctx, i.GetClient(), cluster.ID, cluster.SandboxDomain, i)
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
		if n.NomadNodeShortID == id {
			return n
		}
	}

	return nil
}

func (o *Orchestrator) NodeCount() int {
	return o.nodes.Count()
}
