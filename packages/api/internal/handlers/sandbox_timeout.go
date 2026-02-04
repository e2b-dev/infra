package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDTimeout(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(*types.Team)

	var duration time.Duration

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDTimeoutJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	if body.Timeout < 0 {
		duration = 0
	} else {
		duration = time.Duration(body.Timeout) * time.Second
	}

	sandboxData, err := a.orchestrator.GetSandbox(ctx, team.ID, sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox not found")

		return
	}

	if sandboxData.TeamID != team.ID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID))

		return
	}

	apiErr := a.orchestrator.KeepAliveFor(ctx, team.ID, sandboxID, duration, true)
	if apiErr != nil {
		telemetry.ReportError(ctx, "error when setting timeout", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusNoContent)
}
