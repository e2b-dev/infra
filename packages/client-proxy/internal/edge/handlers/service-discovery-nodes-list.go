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
				Id:        info.ServiceId,
				NodeId:    info.NodeId,
				Status:    getOrchestratorStatusResolved(info.Status),
				Type:      api.ClusterNodeTypeOrchestrator,
				Version:   info.SourceVersion,
				Commit:    info.SourceCommit,
				Host:      info.Host,
				StartedAt: info.Startup,
			},
		)
	}

	// iterate edge apis
	for _, edge := range a.edgePool.GetNodes() {
		info := edge.GetInfo()
		response = append(
			response,
			api.ClusterNode{
				Id:        info.ServiceId,
				NodeId:    info.NodeId,
				Status:    info.Status,
				Type:      api.ClusterNodeTypeEdge,
				Version:   info.SourceVersion,
				Commit:    info.SourceCommit,
				Host:      info.Host,
				StartedAt: info.Startup,
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
