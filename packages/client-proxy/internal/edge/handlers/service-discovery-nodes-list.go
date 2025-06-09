package handlers

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1ServiceDiscoveryNodes(c *gin.Context) {
	response := make([]api.ClusterNode, 0)

	// iterate orchestrator pool
	for _, orchestrator := range a.orchestratorPool.GetOrchestrators() {
		response = append(
			response,
			api.ClusterNode{
				Id:        orchestrator.ServiceId,
				NodeId:    orchestrator.NodeId,
				Status:    getOrchestratorStatusResolved(orchestrator.Status),
				Type:      api.ClusterNodeTypeOrchestrator,
				Version:   orchestrator.SourceVersion,
				Commit:    orchestrator.SourceCommit,
				Host:      orchestrator.Host,
				StartedAt: orchestrator.Startup,
			},
		)
	}

	// iterate edge apis
	for _, edge := range a.edgePool.GetNodes() {
		response = append(
			response,
			api.ClusterNode{
				Id:        edge.ServiceId,
				NodeId:    edge.NodeId,
				Status:    edge.Status,
				Type:      api.ClusterNodeTypeEdge,
				Version:   edge.SourceVersion,
				Commit:    edge.SourceCommit,
				Host:      edge.Host,
				StartedAt: edge.Startup,
			},
		)
	}

	// append itself
	response = append(
		response,
		api.ClusterNode{
			Id:        a.info.ServiceId,
			NodeId:    a.info.NodeId,
			Status:    a.info.GetStatus(),
			Type:      api.ClusterNodeTypeEdge,
			Version:   a.info.SourceVersion,
			Commit:    a.info.SourceCommit,
			Host:      a.info.Host,
			StartedAt: a.info.Startup,
		},
	)

	sort.Slice(
		response,
		func(i, j int) bool {
			// older dates first
			return response[i].StartedAt.Before(response[j].StartedAt)
		},
	)

	c.JSON(http.StatusOK, response)
}
