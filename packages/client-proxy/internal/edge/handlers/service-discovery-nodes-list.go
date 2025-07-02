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
		response = append(
			response,
			api.ClusterNode{
				NodeID:               orchestrator.NodeID,
				ServiceInstanceID:    orchestrator.ServiceInstanceId,
				ServiceStatus:        getOrchestratorStatusResolved(orchestrator.ServiceStatus),
				ServiceType:          api.ClusterNodeTypeOrchestrator,
				ServiceVersion:       orchestrator.ServiceVersion,
				ServiceVersionCommit: orchestrator.ServiceVersionCommit,
				ServiceHost:          orchestrator.Host,
				ServiceStartedAt:     orchestrator.ServiceStartup,
			},
		)
	}

	// iterate edge apis
	for _, edge := range a.edgePool.GetNodes() {
		response = append(
			response,
			api.ClusterNode{
				NodeID:               edge.NodeID,
				ServiceInstanceID:    edge.ServiceInstanceID,
				ServiceStatus:        edge.ServiceStatus,
				ServiceType:          api.ClusterNodeTypeEdge,
				ServiceVersion:       edge.ServiceVersion,
				ServiceVersionCommit: edge.ServiceVersionCommit,
				ServiceHost:          edge.Host,
				ServiceStartedAt:     edge.ServiceStartup,
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
