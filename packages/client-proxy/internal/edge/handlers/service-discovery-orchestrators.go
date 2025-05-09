package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	service_discovery "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
)

func (a *APIStore) V1ServiceDiscoveryGetOrchestrators(c *gin.Context) {
	// ctx := c.Request.Context()

	// todo: later take data from orchestrator pool directly
	nodes, err := a.serviceDiscovery.ListNodes(c)
	if err != nil {
		a.logger.Error("failed to list cluster nodes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to list cluster nodes")
		return
	}

	orchestrators := make([]api.ClusterOrchestratorNode, 0)

	for nodeId, node := range nodes {
		nodeStatus, err := getNodeStatusResolved(node.Status)
		if err != nil {
			a.logger.Error("failed to resolve node status", zap.String("node_id", nodeId), zap.Error(err))
			continue
		}

		if node.ServiceType != service_discovery.ServiceTypeOrchestrator {
			continue
		}

		orchestrators = append(
			orchestrators,
			api.ClusterOrchestratorNode{
				NodeId:      nodeId,
				NodeVersion: node.ServiceVersion,
				NodeStatus:  nodeStatus,

				// todo
				RamMBTotal: 10,
				RamMBUsed:  8,

				VCpuTotal: 8,
				VCpuUsed:  4,
			},
		)
	}

	c.JSON(http.StatusOK, orchestrators)
}
