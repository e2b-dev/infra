package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PatchSandboxesSandboxID(
	c *gin.Context,
	sandboxID api.SandboxID,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	body, err := utils.ParseBody[api.PatchSandboxesSandboxIDJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	// Convert the api.SandboxMetadata to map[string]string if it exists
	var metadata map[string]string
	if body.Metadata != nil {
		metadata = *body.Metadata
	}

	apiErr := a.orchestrator.UpdateSandboxMetadata(ctx, sandboxID, metadata)
	if apiErr != nil {
		telemetry.ReportError(ctx, "error when updating sandbox metadata", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusOK)
}
