package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDSnapshots(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := a.GetTeamInfo(c)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(teamID.String()),
		telemetry.WithSandboxID(sandboxID),
	)

	sandboxID = utils.ShortID(sandboxID)

	sbx, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		var notFoundErr *sandbox.NotFoundError
		if errors.As(err, &notFoundErr) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Sandbox '%s' not found or not running", sandboxID))

			return
		}
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting sandbox")

		return
	}

	if sbx.TeamID != teamID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox '%s'", sandboxID))

		return
	}

	telemetry.ReportEvent(ctx, "Creating snapshot")

	// Snapshot the sandbox - creates template, build, and performs checkpoint atomically
	result, err := a.orchestrator.SnapshotSandbox(ctx, teamID, sandboxID)
	if err != nil {
		if errors.Is(err, orchestrator.ErrSandboxNotRunning) {
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' is not running or is already being snapshotted", sandboxID))

			return
		}
		telemetry.ReportCriticalError(ctx, "Error creating snapshot", err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot")

		return
	}

	c.JSON(http.StatusCreated, api.SnapshotInfo{SnapshotID: result.SnapshotID})
}
