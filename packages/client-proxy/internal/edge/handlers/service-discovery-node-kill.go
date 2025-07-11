package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1ServiceDiscoveryNodeKill(c *gin.Context) {
	ctx := c.Request.Context()

	spanCtx, templateSpan := a.tracer.Start(ctx, "service-discovery-node-kill-handler")
	defer templateSpan.End()

	body, err := parseBody[api.V1ServiceDiscoveryNodeKillJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return
	}

	// requests was for this service instance
	if body.ServiceInstanceID == a.info.ServiceInstanceID && body.ServiceType == api.ClusterNodeTypeEdge {
		a.info.SetStatus(api.Unhealthy)
		c.Status(http.StatusOK)
		return
	}

	reqTimeout, reqTimeoutCancel := context.WithTimeout(spanCtx, 5*time.Second)
	defer reqTimeoutCancel()

	// send request to neighboring node
	err = a.sendNodeRequest(reqTimeout, body.ServiceInstanceID, body.ServiceType, api.Unhealthy)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery node")
		return
	}

	c.Status(http.StatusOK)
}
