package handlers

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1ServiceDiscoveryNodes(c *gin.Context) {
	_, templateSpan := tracer.Start(c, "service-discovery-list-nodes-handler")
	defer templateSpan.End()

	ctx := c.Request.Context()

	response := make([]api.ClusterNode, 0)

	// iterate orchestrator pool
	for _, orchestrator := range a.instancesPool.GetOrchestrators() {
		info := orchestrator.GetInfo()
		response = append(
			response,
			api.ClusterNode{
				NodeID:               info.NodeID,
				ServiceInstanceID:    info.ServiceInstanceID,
				ServiceStatus:        getOrchestratorStatusResolved(ctx, info.ServiceStatus),
				ServiceType:          api.ClusterNodeTypeOrchestrator,
				ServiceVersion:       info.ServiceVersion,
				ServiceVersionCommit: info.ServiceVersionCommit,
				ServiceHost:          info.Host,
				ServiceStartedAt:     info.ServiceStartup,
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
