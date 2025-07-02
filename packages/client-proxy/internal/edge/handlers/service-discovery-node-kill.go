package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1ServiceDiscoveryNodeKill(c *gin.Context, serviceID string) {
	_, templateSpan := a.tracer.Start(c, "service-discovery-node-kill-handler")
	defer templateSpan.End()

	// requests was for this node
	if serviceID == a.info.ServiceInstanceID {
		a.info.SetStatus(api.Unhealthy)
		c.Status(http.StatusOK)
		return
	}

	reqTimeout, reqTimeoutCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer reqTimeoutCancel()

	// send request to neighboring node
	err := a.sendNodeRequest(reqTimeout, serviceID, api.Unhealthy)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery node")
		return
	}

	c.Status(http.StatusOK)
}
