package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	types "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PatchSandboxesSandboxIDMetadata(c *gin.Context, id api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*types.Team)
	team := teamInfo.Team

	body, err := utils.ParseBody[api.PatchSandboxesSandboxIDMetadataJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	if body.Metadata == nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "metadata is required")

		return
	}
	if err := validateAutoResumeMetadata(body.Metadata); err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())

		return
	}

	sandboxID := utils.ShortID(id)
	apiErr := a.orchestrator.UpdateSandboxMetadata(ctx, team.ID, sandboxID, body.Metadata)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportError(ctx, "error updating sandbox metadata", apiErr.Err)

		return
	}

	c.Status(http.StatusNoContent)
}
