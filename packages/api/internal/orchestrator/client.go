package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodes"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const nodeHealthCheckTimeout = time.Second * 2

func (o *Orchestrator) connectToNode(ctx context.Context, discovered nodes.NomadServiceDiscovery) error {
	ctx, childSpan := o.tracer.Start(ctx, "connect-to-node")
	defer childSpan.End()

	orchestratorNode, err := nodes.New(ctx, o.tel.TracerProvider, o.tel.MeterProvider, discovered)
	if err != nil {
		return err
	}

	// Update host metrics from service info
	o.registerNode(orchestratorNode)
	return nil
}

func (o *Orchestrator) connectToClusterNode(ctx context.Context, cluster *edge.Cluster, i *edge.ClusterInstance) {
	// this way we don't need to worry about multiple clusters with the same node ID in shared pool
	clusterGRPC := cluster.GetGRPC(i.ServiceInstanceID)

	orchestratorNode, err := nodes.NewClusterNode(ctx, clusterGRPC.Client, cluster.ID, i)
	if err != nil {
		zap.L().Error("Failed to create node", zap.Error(err))
		return
	}

	o.registerNode(orchestratorNode)
}

func (o *Orchestrator) registerNode(node *nodes.Node) {
	scopedKey := o.scopedNodeID(node.ClusterID, node.ID)
	o.nodes.Insert(scopedKey, node)
}

func (o *Orchestrator) deregisterNode(node *nodes.Node) {
	scopedKey := o.scopedNodeID(node.ClusterID, node.ID)
	o.nodes.Remove(scopedKey)
}

// When prefixed with cluster ID, node is unique in the map containing nodes from multiple clusters
func (o *Orchestrator) scopedNodeID(clusterID uuid.UUID, nodeID string) string {
	if clusterID == uuid.Nil {
		return nodeID
	}

	return fmt.Sprintf("%s-%s", clusterID.String(), nodeID)
}

func (o *Orchestrator) GetClient(ctx context.Context, clusterID uuid.UUID, nodeID string) (*grpclient.GRPCClient, context.Context, error) {
	n := o.GetNode(clusterID, nodeID)
	if n == nil {
		return nil, nil, fmt.Errorf("node '%s' not found in cluster '%s'", nodeID, clusterID)
	}

	client, ctx := n.GetClient(ctx)
	return client, ctx, nil
}

func (o *Orchestrator) listNomadNodes(ctx context.Context) ([]nodes.NomadServiceDiscovery, error) {
	_, listSpan := o.tracer.Start(ctx, "list-nomad-nodes")
	defer listSpan.End()

	options := &nomadapi.QueryOptions{
		// TODO: Use variable for node pool name ("default")
		Filter: "Status == \"ready\" and NodePool == \"default\"",
	}
	nomadNodes, _, err := o.nomadClient.Nodes().List(options.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	result := make([]nodes.NomadServiceDiscovery, 0, len(nomadNodes))
	for _, n := range nomadNodes {
		result = append(result, nodes.NomadServiceDiscovery{
			NomadNodeShortID:    n.ID[:consts.NodeIDLength],
			OrchestratorAddress: fmt.Sprintf("%s:%s", n.Address, consts.OrchestratorPort),
			IPAddress:           n.Address,
		})
	}

	return result, nil
}

func (o *Orchestrator) GetNode(clusterID uuid.UUID, nodeID string) *nodes.Node {
	scopedKey := o.scopedNodeID(clusterID, nodeID)
	n, _ := o.nodes.Get(scopedKey)
	return n
}

func (o *Orchestrator) GetClusterNodes(clusterID uuid.UUID) []*nodes.Node {
	clusterNodes := make([]*nodes.Node, 0)
	for _, n := range o.nodes.Items() {
		if n.ClusterID == clusterID {
			clusterNodes = append(clusterNodes, n)
		}
	}

	return clusterNodes
}

// Deprecated: use GetNode instead
func (o *Orchestrator) GetNodeByNomadShortID(id string) *nodes.Node {
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
