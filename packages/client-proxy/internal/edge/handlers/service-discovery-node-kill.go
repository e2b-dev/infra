package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
)

func (a *APIStore) V1ServiceDiscoveryNodeKill(c *gin.Context, serviceId string) {
	// requests was for this node
	if serviceId == a.info.ServiceId {
		a.info.SetStatus(api.Unhealthy)
		c.Status(http.StatusOK)
		return
	}

	reqTimeout, reqTimeoutCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer reqTimeoutCancel()

	// send request to neighboring node
	err := a.sendNodeRequest(reqTimeout, serviceId, api.Unhealthy)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery node")
		return
	}

	c.Status(http.StatusOK)
}
