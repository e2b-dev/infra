package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/pause"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDPause(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	// Get team from context, use TeamContextKey

	teamID := auth.MustGetTeamInfo(c).Team.ID

	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(telemetry.WithSandboxID(sandboxID))

	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	pause.LogInitiated(ctx, sandboxID, teamID.String(), pause.ReasonRequest)

	err = a.orchestrator.RemoveSandbox(ctx, teamID, sandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	var transErr *sandbox.InvalidStateTransitionError

	switch {
	case err == nil:
		pause.LogSuccess(ctx, sandboxID, teamID.String(), pause.ReasonRequest)
	case errors.Is(err, orchestrator.ErrSandboxNotFound):
		apiErr := pauseHandleNotRunningSandbox(ctx, a.snapshotCache, sandboxID, teamID)
		switch apiErr.Code {
		case http.StatusConflict:
			pause.LogSkipped(ctx, sandboxID, teamID.String(), pause.ReasonRequest, pause.SkipReasonAlreadyPaused)
		case http.StatusNotFound:
			pause.LogSkipped(ctx, sandboxID, teamID.String(), pause.ReasonRequest, pause.SkipReasonNotFound)
		default:
			pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonRequest, err)
		}
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	case errors.As(err, &transErr):
		pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonRequest, err)
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' cannot be paused while in '%s' state", sandboxID, transErr.CurrentState))

		return
	default:
		pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonRequest, err)
		telemetry.ReportError(ctx, "error pausing sandbox", err)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error pausing sandbox")

		return
	}

	c.Status(http.StatusNoContent)
}

func pauseHandleNotRunningSandbox(ctx context.Context, cache *snapshotcache.SnapshotCache, sandboxID string, teamID uuid.UUID) api.APIError {
	// TODO: ENG-3544 scope GetLastSnapshot query by teamID to avoid post-fetch ownership check.
	snap, err := cache.Get(ctx, sandboxID)
	if err == nil {
		if snap.Snapshot.TeamID != teamID {
			logger.L().Debug(ctx, "Snapshot team mismatch on pause", logger.WithSandboxID(sandboxID), logger.WithTeamID(teamID.String()))

			return api.APIError{
				Code:      http.StatusNotFound,
				ClientMsg: utils.SandboxNotFoundMsg(sandboxID),
			}
		}

		logger.L().Warn(ctx, "Sandbox is already paused", logger.WithSandboxID(sandboxID))

		return api.APIError{
			Code:      http.StatusConflict,
			ClientMsg: fmt.Sprintf("Error pausing sandbox - sandbox '%s' is already paused", sandboxID),
		}
	}

	if errors.Is(err, snapshotcache.ErrSnapshotNotFound) {
		logger.L().Debug(ctx, "Snapshot not found", logger.WithSandboxID(sandboxID))

		return api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: utils.SandboxNotFoundMsg(sandboxID),
		}
	}

	logger.L().Error(ctx, "Error getting snapshot", zap.Error(err), logger.WithSandboxID(sandboxID))

	return api.APIError{
		Code:      http.StatusInternalServerError,
		ClientMsg: "Error pausing sandbox",
	}
}
