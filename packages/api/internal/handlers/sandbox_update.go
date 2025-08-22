package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PutSandboxesSandboxIDMetadata(
	c *gin.Context,
	sandboxID api.SandboxID,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	// Get team from context
	teamID := a.GetTeamInfo(c).Team.ID

	metadata, err := utils.ParseBody[api.PutSandboxesSandboxIDMetadataJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithSandboxID(sandboxID),
		telemetry.WithTeamID(teamID.String()),
	)

	sbx, err := a.orchestrator.GetSandbox(sandboxID)
	if err == nil {
		// Verify the sandbox belongs to the team
		if sbx.TeamID != teamID {
			telemetry.ReportCriticalError(ctx, fmt.Sprintf("sandbox '%s' doesn't belong to team '%s'", sandboxID, teamID.String()), nil)
			a.sendAPIStoreError(c, http.StatusUnauthorized, fmt.Sprintf("Error updating sandbox - sandbox '%s' does not belong to your team '%s'", sandboxID, teamID.String()))

			return
		}

		sbx.Lock()
		defer sbx.Unlock()

		apiErr := a.orchestrator.UpdateSandboxMetadata(ctx, sbx, metadata)
		if apiErr != nil {
			telemetry.ReportError(ctx, "error when updating sandbox metadata", apiErr.Err)
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}
	} else {
		// Sandbox not found in cache, might be paused
		// Try to update the snapshot metadata in the database
		zap.L().Debug("Sandbox not found in cache, checking if it's paused",
			logger.WithSandboxID(sandboxID),
			logger.WithTeamID(teamID.String()))

		updated, err := a.sqlcDB.UpdateSnapshotMetadata(ctx, queries.UpdateSnapshotMetadataParams{
			SandboxID: sandboxID,
			TeamID:    teamID,
			Metadata:  types.JSONBStringMap(metadata),
		})
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when updating paused sandbox metadata", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating paused sandbox metadata: %s", err))

			return
		}

		if len(updated) == 0 {
			telemetry.ReportCriticalError(ctx, "sandbox not found", nil)
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Sandbox '%s' not found", sandboxID))

			return
		}

		telemetry.ReportEvent(ctx, "Updated paused sandbox metadata")
	}

	c.Status(http.StatusOK)
}
