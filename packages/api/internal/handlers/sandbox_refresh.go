package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

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

	apiErr := a.orchestrator.KeepAliveFor(ctx, sandboxID, duration, false)
	if apiErr != nil {
		zap.L().Debug("Error when refreshing sandbox", zap.Error(apiErr.Err), zap.String("sandbox_id", sandboxID))
		telemetry.ReportCriticalError(ctx, apiErr.Err)

		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		return
	}

	c.Status(http.StatusNoContent)
}
