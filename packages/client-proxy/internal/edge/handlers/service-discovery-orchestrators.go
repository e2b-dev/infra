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
	_, templateSpan := a.tracer.Start(c, "service-discovery-list-orchestrators-handler")
	defer templateSpan.End()

	response := make([]api.ClusterOrchestratorNode, 0)

	for _, node := range a.orchestratorPool.GetOrchestrators() {
		info := node.GetInfo()
		response = append(
			response,
			api.ClusterOrchestratorNode{
				NodeID:            info.NodeID,
				ServiceInstanceID: info.ServiceInstanceID,

				ServiceVersion:       info.ServiceVersion,
				ServiceVersionCommit: info.ServiceVersionCommit,
				ServiceHost:          info.Host,
				ServiceStartedAt:     info.ServiceStartup,
				ServiceStatus:        getOrchestratorStatusResolved(info.ServiceStatus),

				Roles: getOrchestratorRolesResolved(info.Roles),

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
			return response[i].ServiceStartedAt.Before(response[j].ServiceStartedAt)
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
		case e2bgrpcorchestratorinfo.ServiceInfoRole_TemplateBuilder:
			roles = append(roles, api.ClusterOrchestratorRoleTemplateBuilder)
		default:
			zap.L().Error("Unknown orchestrator role", zap.String("role", string(role)))
		}
	}

	return roles
}
