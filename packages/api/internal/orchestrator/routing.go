package orchestrator

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const localSandboxRouteHost = "127.0.0.1"

func routeNodeIPAddress(node *nodemanager.Node, local bool) string {
	if node.IPAddress != "" {
		return node.IPAddress
	}

	if local && node.ClusterID == consts.LocalClusterID {
		return localSandboxRouteHost
	}

	return ""
}

func currentRouteNodeIPAddress(node *nodemanager.Node) string {
	return routeNodeIPAddress(node, env.IsLocal())
}

func (o *Orchestrator) GetNodeRouteIPAddress(clusterID uuid.UUID, nodeID string) (string, bool) {
	node := o.GetNode(clusterID, nodeID)
	if node == nil {
		return "", false
	}

	nodeIP := currentRouteNodeIPAddress(node)

	return nodeIP, nodeIP != ""
}
