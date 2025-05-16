package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func (a *APIStore) V1ServiceDiscoveryNodeKill(c *gin.Context, nodeId string) {
	err := a.sendNodeRequest(c, nodeId, orchestratorinfo.ServiceInfoStatus_OrchestratorUnhealthy)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery node")
		return
	}

	c.Status(http.StatusOK)
}
