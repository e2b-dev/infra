package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PatchSandboxesSandboxIDLifecycle(c *gin.Context, id api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*types.Team)
	team := teamInfo.Team

	body, err := utils.ParseBody[api.PatchSandboxesSandboxIDLifecycleJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	sandboxID := utils.ShortID(id)
	apiErr := a.orchestrator.UpdateSandboxLifecycle(ctx, team.ID, sandboxID, body.AutoPause)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportError(ctx, "error updating sandbox lifecycle", apiErr.Err)

		return
	}

	c.Status(http.StatusNoContent)
}
