package handlers

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	e2bgrpcorchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1ServiceDiscoveryGetOrchestrators(c *gin.Context) {
	response := make([]api.ClusterOrchestratorNode, 0)

	for _, node := range a.orchestratorPool.GetOrchestrators() {
		response = append(
			response,
			api.ClusterOrchestratorNode{
				Id:        node.ServiceId,
				NodeId:    node.NodeId,
				Version:   node.SourceVersion,
				Commit:    node.SourceCommit,
				StartedAt: node.Startup,
				Status:    getOrchestratorStatusResolved(node.Status),
				Roles:     getOrchestratorRolesResolved(node.Roles),

				MetricRamMBUsed:        node.MetricMemoryUsedInMB.Load(),
				MetricVCpuUsed:         node.MetricVCpuUsed.Load(),
				MetricDiskMBUsed:       node.MetricDiskUsedInMB.Load(),
				MetricSandboxesRunning: node.MetricSandboxesRunning.Load(),
			},
		)
	}

	sort.Slice(
		response,
		func(i, j int) bool {
			// older dates first
			return response[i].StartedAt.Before(response[j].StartedAt)
		},
	)

	c.JSON(http.StatusOK, response)
}

func getOrchestratorStatusResolved(s e2borchestrators.OrchestratorStatus) api.ClusterNodeStatus {
	switch s {
	case e2borchestrators.OrchestratorStatusHealthy:
		return api.Healthy
	case e2borchestrators.OrchestratorStatusDraining:
		return api.Draining
	case e2borchestrators.OrchestratorStatusUnhealthy:
		return api.Unhealthy
	default:
		zap.L().Error("Unknown orchestrator status", zap.String("status", string(s)))
		return api.Unhealthy
	}
}

func getOrchestratorRolesResolved(r []e2bgrpcorchestratorinfo.ServiceInfoRole) []api.ClusterOrchestratorRole {
	roles := make([]api.ClusterOrchestratorRole, 0)

	for _, role := range r {
		switch role {
		case e2bgrpcorchestratorinfo.ServiceInfoRole_Orchestrator:
			roles = append(roles, api.ClusterOrchestratorRoleOrchestrator)
		case e2bgrpcorchestratorinfo.ServiceInfoRole_TemplateManager:
			roles = append(roles, api.ClusterOrchestratorRoleTemplateManager)
		default:
			zap.L().Error("Unknown orchestrator role", zap.String("role", string(role)))
		}
	}

	return roles
}
