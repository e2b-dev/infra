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
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
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

	sbx, err := a.orchestrator.GetSandboxData(sandboxID, true)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", sandboxID))
		return
	}

	if sbx.TeamID != teamID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID))
		return
	}

	err = a.orchestrator.RemoveSandbox(ctx, sbx, instance.StateActionPause)
	switch {
	case err == nil:
	case errors.Is(err, orchestrator.ErrSandboxNotFound):
		_, fErr := a.sqlcDB.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{SandboxID: sandboxID, TeamID: teamID})
		if fErr == nil {
			zap.L().Warn("Sandbox is already paused", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Error pausing sandbox - sandbox '%s' is already paused", sandboxID))
			return
		}

		if errors.Is(fErr, sql.ErrNoRows) {
			zap.L().Debug("Snapshot not found", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error pausing sandbox - snapshot for sandbox '%s' was not found", sandboxID))
			return
		}

		zap.L().Error("Error getting snapshot", zap.Error(fErr), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error pausing sandbox")
		return
	default:
		telemetry.ReportError(ctx, "error pausing sandbox", err)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error pausing sandbox")
		return
	}

	c.Status(http.StatusNoContent)
}
