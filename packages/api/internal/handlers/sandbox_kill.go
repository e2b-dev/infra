package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) DeleteSandboxesSandboxID(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	teamID := c.Value(auth.TeamContextKey).(models.Team).ID

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		attribute.String("team.id", teamID.String()),
	)

	err := a.orchestrator.DeleteInstanceRequest(ctx, sandboxID, nil)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting sandbox")
		telemetry.ReportCriticalError(ctx, err)
		return
	}

	c.Status(http.StatusNoContent)
}
