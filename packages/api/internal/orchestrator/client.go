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
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
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

func (o *Orchestrator) connectToClusterNode(ctx context.Context, cluster *edge.Cluster, i *edge.ClusterInstance) {
	// this way we don't need to worry about multiple clusters with the same node ID in shared pool
	clusterGRPC := cluster.GetGRPC(i.ServiceInstanceID)

	orchestratorNode, err := nodemanager.NewClusterNode(ctx, clusterGRPC.Client, cluster.ID, cluster.SandboxDomain, i)
	if err != nil {
		zap.L().Error("Failed to create node", zap.Error(err))

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

func (o *Orchestrator) GetClient(ctx context.Context, clusterID uuid.UUID, nodeID string) (*grpclient.GRPCClient, context.Context, error) {
	n := o.GetNode(clusterID, nodeID)
	if n == nil {
		return nil, nil, fmt.Errorf("node '%s' not found in cluster '%s'", nodeID, clusterID)
	}

	client, ctx := n.GetClient(ctx)

	return client, ctx, nil
}

func (o *Orchestrator) listNomadNodes(ctx context.Context) ([]nodemanager.NomadServiceDiscovery, error) {
	_, listSpan := tracer.Start(ctx, "list-nomad-nodes")
	defer listSpan.End()

	options := &nomadapi.QueryOptions{
		Filter: `ClientStatus == "running" and JobID contains "orchestrator-"`,
		Params: map[string]string{"resources": "true"},
	}
	nomadAllocations, _, err := o.nomadClient.Allocations().List(options.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	result := make([]nodemanager.NomadServiceDiscovery, 0, len(nomadAllocations))
	for _, alloc := range nomadAllocations {
		if !isHealthy(alloc) {
			zap.L().Debug("Skipping unhealthy allocation", zap.String("allocation_id", alloc.ID))

			continue
		}

		ip, port, ok := o.findPortInAllocation(alloc)
		if !ok {
			zap.L().Warn("Cannot find port in allocation",
				zap.String("allocation_id", alloc.ID), zap.String("port_name", "grpc"))

			continue
		}

		zap.L().Debug("Found port in allocation",
			zap.String("allocation_id", alloc.ID),
			zap.String("port_name", "grpc"),
			zap.String("ip", ip),
			zap.Int("port", port),
		)

		result = append(result, nodemanager.NomadServiceDiscovery{
			NomadNodeShortID:    alloc.NodeID[:consts.NodeIDLength],
			OrchestratorAddress: fmt.Sprintf("%s:%d", ip, port),
			IPAddress:           ip,
		})
	}

	return result, nil
}

func isHealthy(alloc *nomadapi.AllocationListStub) bool {
	if alloc == nil {
		zap.L().Warn("Allocation is nil")

		return false
	}

	if alloc.DeploymentStatus == nil {
		zap.L().Warn("Allocation deployment status is nil", zap.String("allocation_id", alloc.ID))

		return true
	}

	if alloc.DeploymentStatus.Healthy == nil {
		zap.L().Warn("Allocation deployment status healthy is nil", zap.String("allocation_id", alloc.ID))

		return true
	}

	return *alloc.DeploymentStatus.Healthy
}

func (o *Orchestrator) findPortInAllocation(allocation *nomadapi.AllocationListStub) (string, int, bool) {
	if allocation == nil {
		return "", 0, false
	}

	if allocation.AllocatedResources == nil {
		return "", 0, false
	}

	for _, task := range allocation.AllocatedResources.Tasks {
		for _, network := range task.Networks {
			host, port, ok := o.findPortInNetwork(network)
			if ok {
				return host, port, true
			}
		}
	}

	for _, net := range allocation.AllocatedResources.Shared.Networks {
		host, port, ok := o.findPortInNetwork(net)
		if ok {
			return host, port, true
		}
	}

	return "", 0, false
}

func (o *Orchestrator) findPortInNetwork(net *nomadapi.NetworkResource) (string, int, bool) {
	for _, port := range net.ReservedPorts {
		if port.Label == o.portLabel {
			return net.IP, port.Value, true
		}

		if port.Value == o.defaultPort {
			return net.IP, o.defaultPort, true
		}
	}

	for _, port := range net.DynamicPorts {
		if port.Label == o.portLabel {
			return net.IP, port.Value, true
		}

		if port.Value == o.defaultPort {
			return net.IP, o.defaultPort, true
		}
	}

	return "", 0, false
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
