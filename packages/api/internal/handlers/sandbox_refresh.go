package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDRefreshes(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	var duration time.Duration

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDRefreshesJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	if body.Duration == nil {
		duration = instance.InstanceExpiration
	} else {
		duration = time.Duration(*body.Duration) * time.Second
	}

	if duration < instance.InstanceExpiration {
		duration = instance.InstanceExpiration
	}

	sandbox, err := a.orchestrator.GetSandbox(sandboxID)
	if err != nil {
		telemetry.ReportError(ctx, "error when getting sandbox", err)
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Sandbox '%s' not found", sandboxID))

		return
	}

	sandbox.Lock()
	defer sandbox.Unlock()

	apiErr := a.orchestrator.KeepAliveFor(ctx, sandbox, duration, false)
	if apiErr != nil {
		telemetry.ReportError(ctx, "error when refreshing sandbox", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusNoContent)
}
