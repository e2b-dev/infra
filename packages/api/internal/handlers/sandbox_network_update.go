package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PutSandboxesSandboxIDNetwork(
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

	body, err := utils.ParseBody[api.PutSandboxesSandboxIDNetworkJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	var allowedCIDRs []string
	if body.AllowOut != nil {
		allowedCIDRs = *body.AllowOut
	}

	var deniedCIDRs []string
	if body.DenyOut != nil {
		deniedCIDRs = *body.DenyOut
	}

	_, apiErr := a.orchestrator.UpdateSandboxNetworkConfig(ctx, team.ID, sandboxID, allowedCIDRs, deniedCIDRs)
	if apiErr != nil {
		telemetry.ReportError(ctx, "error updating sandbox network config", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusNoContent)
}
