package handlers

import (
	"github.com/gin-gonic/gin"
)

func (a *APIStore) V1ServiceDiscoveryGetOrchestrators(c *gin.Context) {
	/*
		nodes, err := a.orchestratorsPool.GetOrchestrators()
		if err != nil {
			a.logger.Error("failed to list cluster nodes", zap.Error(err))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to list cluster nodes")
			return
		}

		orchestrators := make([]api.ClusterOrchestratorNode, 0)

		for _, node := range nodes {
			nodeStatus, err := getNodeStatusResolved(node.Status)
			if err != nil {
				a.logger.Error("failed to resolve node status", zap.String("node_id", node.Id), zap.Error(err))
				continue
			}

			orchestrators = append(
				orchestrators,
				api.ClusterOrchestratorNode{
					NodeId:      node.Id,
					NodeVersion: node.Version,
					NodeStatus:  nodeStatus,

					CanSpawn: node.CanSpawnSandboxes,
					CanBuild: node.CanBuildTemplates,

					MetricRamMBUsed:        node.MemoryUsedInMB.Load(),
					MetricVCpuUsed:         node.VCpuUsed.Load(),
					MetricDiskMBUsed:       node.DiskUsedInMB.Load(),
					MetricSandboxesRunning: node.SandboxesRunning.Load(),
				},
			)
		}

		c.JSON(http.StatusOK, orchestrators)

	*/
}
