package orchestrator

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const localSandboxIPAddress = "127.0.0.1"

func routeNodeIPAddress(node *nodemanager.Node, local bool) string {
	if node.IPAddress != "" {
		return node.IPAddress
	}

	if local && node.ClusterID == consts.LocalClusterID {
		return localSandboxIPAddress
	}

	return ""
}

func sandboxRoutingMetadata(node *nodemanager.Node, local bool) *sandbox.RoutingMetadata {
	// Remote clusters register routes through gRPC metadata instead.
	if node.IsClusterNode() {
		return nil
	}

	return &sandbox.RoutingMetadata{
		OrchestratorID: node.Metadata().ServiceInstanceID,
		OrchestratorIP: routeNodeIPAddress(node, local),
	}
}

func (o *Orchestrator) GetNodeRouteIPAddress(clusterID uuid.UUID, nodeID string) string {
	node := o.GetNode(clusterID, nodeID)
	if node == nil {
		return ""
	}

	return routeNodeIPAddress(node, env.IsLocal())
}
