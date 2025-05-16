package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/gin-gonic/gin"
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

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

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

	err = a.orchestrator.KeepAliveFor(ctx, sandboxID, duration, false)
	if err != nil {
		errMsg := fmt.Errorf("error when refreshing sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error refreshing sandbox '%s'", sandboxID))

		return
	}

	c.Status(http.StatusNoContent)
}
