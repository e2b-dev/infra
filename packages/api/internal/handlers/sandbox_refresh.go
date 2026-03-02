package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDRefreshes(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()

	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	team := auth.MustGetTeamInfo(c)
	var duration time.Duration

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDRefreshesJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	if body.Duration == nil {
		duration = sandbox.SandboxTimeoutDefault
	} else {
		duration = time.Duration(*body.Duration) * time.Second
	}

	if duration < sandbox.SandboxTimeoutDefault {
		duration = sandbox.SandboxTimeoutDefault
	}

	sandboxData, err := a.orchestrator.GetSandbox(ctx, team.ID, sandboxID)
	if err != nil {
		logger.L().Debug(ctx, "Sandbox not found for refresh", logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusNotFound, sandboxNotFoundMsg(sandboxID))

		return
	}

	if sandboxData.TeamID != team.ID {
		logger.L().Debug(ctx, "Sandbox team mismatch on refresh", logger.WithSandboxID(sandboxID), logger.WithTeamID(team.ID.String()))
		a.sendAPIStoreError(c, http.StatusNotFound, sandboxNotFoundMsg(sandboxID))

		return
	}

	apiErr := a.orchestrator.KeepAliveFor(ctx, team.ID, sandboxID, duration, false)
	if apiErr != nil {
		telemetry.ReportError(ctx, "error when refreshing sandbox", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusNoContent)
}
