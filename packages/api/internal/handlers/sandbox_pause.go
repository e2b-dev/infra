package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDPause(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	// Get team from context, use TeamContextKey

	teamID := a.GetTeamInfo(c).Team.ID

	sandboxID = utils.ShortID(sandboxID)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sbx, err := a.orchestrator.GetSandbox(sandboxID)
	if err != nil {
		_, fErr := a.sqlcDB.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{SandboxID: sandboxID, TeamID: teamID})
		if fErr == nil {
			zap.L().Warn("Sandbox is already paused", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Error pausing sandbox - sandbox '%s' is already paused", sandboxID))
			return
		}

		if errors.Is(err, sql.ErrNoRows) {
			zap.L().Debug("Snapshot not found", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error pausing sandbox - snapshot for sandbox '%s' was not found", sandboxID))
			return
		}

		zap.L().Error("Error getting snapshot", zap.Error(fErr), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error pausing sandbox")
		return
	}

	if *sbx.TeamID != teamID {
		telemetry.ReportCriticalError(ctx, "sandbox does not belong to team", fmt.Errorf("sandbox '%s' does not belong to team '%s'", sandboxID, teamID.String()))

		a.sendAPIStoreError(c, http.StatusUnauthorized, fmt.Sprintf("Error pausing sandbox - sandbox '%s' does not belong to your team '%s'", sandboxID, teamID.String()))

		return
	}

	found := a.orchestrator.DeleteInstance(ctx, sandboxID, true)
	if !found {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error pausing sandbox - sandbox '%s' was not found", sandboxID))
		return
	}

	_, err = sbx.Pausing.WaitWithContext(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error pausing sandbox: %s", err))

		return
	}

	c.Status(http.StatusNoContent)
}
