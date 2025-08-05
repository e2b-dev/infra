package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PutSandboxesSandboxIDMetadata(
	c *gin.Context,
	sandboxID api.SandboxID,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	metadata, err := utils.ParseBody[api.PutSandboxesSandboxIDMetadataJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	apiErr := a.orchestrator.UpdateSandboxMetadata(ctx, sandboxID, metadata)
	if apiErr != nil {
		telemetry.ReportError(ctx, "error when updating sandbox metadata", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusOK)
}
