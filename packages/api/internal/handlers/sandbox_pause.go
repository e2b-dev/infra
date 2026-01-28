package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
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

	sbx, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		apiErr := pauseHandleNotRunningSandbox(ctx, a.sqlcDB, sandboxID, teamID)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	if sbx.TeamID != teamID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID))

		return
	}

	err = a.orchestrator.RemoveSandbox(ctx, sbx, sandbox.StateActionPause)
	switch {
	case err == nil:
	case errors.Is(err, orchestrator.ErrSandboxNotFound):
		apiErr := pauseHandleNotRunningSandbox(ctx, a.sqlcDB, sandboxID, teamID)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	default:
		telemetry.ReportError(ctx, "error pausing sandbox", err)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error pausing sandbox")

		return
	}

	c.Status(http.StatusNoContent)
}

func pauseHandleNotRunningSandbox(ctx context.Context, sqlcDB *sqlcdb.Client, sandboxID string, teamID uuid.UUID) api.APIError {
	snap, err := sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err == nil {
		if snap.Snapshot.TeamID != teamID {
			return api.APIError{
				Code:      http.StatusForbidden,
				ClientMsg: fmt.Sprintf("You don't have access to sandbox '%s'", sandboxID),
			}
		}

		logger.L().Warn(ctx, "Sandbox is already paused", logger.WithSandboxID(sandboxID))

		return api.APIError{
			Code:      http.StatusConflict,
			ClientMsg: fmt.Sprintf("Error pausing sandbox - sandbox '%s' is already paused", sandboxID),
		}
	}

	if errors.Is(err, sql.ErrNoRows) {
		logger.L().Debug(ctx, "Snapshot not found", logger.WithSandboxID(sandboxID))

		return api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: fmt.Sprintf("Error pausing sandbox - snapshot for sandbox '%s' was not found", sandboxID),
		}
	}

	logger.L().Error(ctx, "Error getting snapshot", zap.Error(err), logger.WithSandboxID(sandboxID))

	return api.APIError{
		Code:      http.StatusInternalServerError,
		ClientMsg: "Error pausing sandbox",
	}
}
