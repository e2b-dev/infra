package handlers

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1ServiceDiscoveryNodes(c *gin.Context) {
	_, templateSpan := a.tracer.Start(c, "service-discovery-list-nodes-handler")
	defer templateSpan.End()

	response := make([]api.ClusterNode, 0)

	// iterate orchestrator pool
	for _, orchestrator := range a.orchestratorPool.GetOrchestrators() {
		info := orchestrator.GetInfo()
		response = append(
			response,
			api.ClusterNode{
				NodeID:               info.NodeID,
				ServiceInstanceID:    info.ServiceInstanceID,
				ServiceStatus:        getOrchestratorStatusResolved(info.ServiceStatus),
				ServiceType:          api.ClusterNodeTypeOrchestrator,
				ServiceVersion:       info.ServiceVersion,
				ServiceVersionCommit: info.ServiceVersionCommit,
				ServiceHost:          info.Host,
				ServiceStartedAt:     info.ServiceStartup,
			},
		)
	}

	// iterate edge apis
	for _, edge := range a.edgePool.GetInstances() {
		info := edge.GetInfo()
		response = append(
			response,
			api.ClusterNode{
				NodeID:               info.NodeID,
				ServiceInstanceID:    info.ServiceInstanceID,
				ServiceStatus:        info.ServiceStatus,
				ServiceType:          api.ClusterNodeTypeEdge,
				ServiceVersion:       info.ServiceVersion,
				ServiceVersionCommit: info.ServiceVersionCommit,
				ServiceHost:          info.Host,
				ServiceStartedAt:     info.ServiceStartup,
			},
		)
	}

	// append itself
	response = append(
		response,
		api.ClusterNode{
			NodeID:               a.info.NodeID,
			ServiceInstanceID:    a.info.ServiceInstanceID,
			ServiceStatus:        a.info.GetStatus(),
			ServiceType:          api.ClusterNodeTypeEdge,
			ServiceVersion:       a.info.ServiceVersion,
			ServiceVersionCommit: a.info.ServiceVersionCommit,
			ServiceHost:          a.info.Host,
			ServiceStartedAt:     a.info.ServiceStartup,
		},
	)

	sort.Slice(
		response,
		func(i, j int) bool {
			// older dates first
			return response[i].ServiceStartedAt.Before(response[j].ServiceStartedAt)
		},
	)

	c.JSON(http.StatusOK, response)
}
