package handlers

import (
	"fmt"
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
	for _, orchestrator := range a.orchestratorPool.GetOrchestrators() {
		info := orchestrator.GetInfo()
		response = append(
			response,
			api.ClusterNode{
				NodeID:               info.NodeID,
				ServiceInstanceID:    info.InstanceID,
				ServiceStatus:        getOrchestratorStatusResolved(ctx, info.Status),
				ServiceType:          api.ClusterNodeTypeOrchestrator,
				ServiceVersion:       info.Version,
				ServiceVersionCommit: info.VersionCommit,
				ServiceHost:          fmt.Sprintf("%s:%d", info.IPAddress, info.ApiPort),
				ServiceStartedAt:     info.Startup,
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
