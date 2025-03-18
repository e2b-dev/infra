package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
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
		_, _, fErr := a.db.GetLastSnapshot(ctx, sandboxID, teamID)
		if fErr == nil {
			zap.L().Warn("Sandbox is already paused", zap.String("sandboxID", sandboxID))
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Error pausing sandbox - sandbox '%s' is already paused", sandboxID))
			return
		}

		var errNotFound db.ErrNotFound
		if errors.Is(err, errNotFound) {
			zap.L().Debug("Snapshot not found", zap.String("sandboxID", sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error pausing sandbox - snapshot for sandbox '%s' was not found", sandboxID))
			return
		}

		zap.L().Error("Error getting snapshot", zap.Error(fErr), zap.String("sandboxID", sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error pausing sandbox"))
		return
	}

	if *sbx.TeamID != teamID {
		errMsg := fmt.Errorf("sandbox '%s' does not belong to team '%s'", sandboxID, teamID.String())
		telemetry.ReportCriticalError(ctx, errMsg)

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
