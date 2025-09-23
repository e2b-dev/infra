package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDClone(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	// Get team from context, use TeamContextKey

	teamID := a.GetTeamInfo(c).Team.ID

	sandboxID = utils.ShortID(sandboxID)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDCloneJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	timeout := instance.InstanceExpiration
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Tier.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Tier.MaxLengthHours))

			return
		}
	}

	isOriginalSbxRunning := true

	originalSbx, err := a.orchestrator.GetSandboxData(sandboxID, false)
	if err != nil {
		zap.L().Warn("Original sandbox not for clone not found", zap.Error(err), logger.WithSandboxID(sandboxID))

		isOriginalSbxRunning = false
	}

	if isOriginalSbxRunning {
		if originalSbx.TeamID != teamID {
			a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID))

			return
		}

		err = a.orchestrator.RemoveSandbox(ctx, originalSbx, instance.StateActionPause)
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
	}

	// Paused sandbox, now we need to start it twice if the original sandbox is running, otherwise just start the cloned sandbox.
	originalSbxCache, err := a.orchestrator.GetSandbox(sandboxID)
	if err == nil {
		data := originalSbxCache.Data()
		switch data.State {
		case instance.StatePausing:
			zap.L().Debug("Waiting for sandbox to pause", logger.WithSandboxID(sandboxID))
			err = originalSbxCache.WaitForStateChange(ctx)
			if err != nil {
				a.sendAPIStoreError(c, http.StatusInternalServerError, "Error waiting for sandbox to pause")

				return
			}
		case instance.StateKilling:
			a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox can't be resumed, no snapshot found")

			return
		case instance.StateRunning:
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox %s is already running", sandboxID))

			zap.L().Debug("Sandbox is already running",
				logger.WithSandboxID(sandboxID),
				zap.Time("end_time", data.EndTime),
				zap.Time("start_time", data.StartTime),
				zap.String("node_id", data.NodeID),
			)

			return
		default:
			zap.L().Error("Sandbox is in an unknown state", logger.WithSandboxID(sandboxID), zap.String("state", string(data.State)))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Sandbox is in an unknown state")

			return
		}
	}

	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{SandboxID: sandboxID, TeamID: teamInfo.Team.ID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			zap.L().Debug("Snapshot not found", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox can't be resumed, no snapshot found")
			return
		}

		zap.L().Error("Error getting last snapshot", logger.WithSandboxID(sandboxID), zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting snapshot")

		return
	}

	autoPause := lastSnapshot.Snapshot.AutoPause

	snap := lastSnapshot.Snapshot
	build := lastSnapshot.EnvBuild

	nodeID := &snap.OriginNodeID

	alias := ""
	if len(lastSnapshot.Aliases) > 0 {
		alias = lastSnapshot.Aliases[0]
	}

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: build.EnvID,
		TeamID:     teamInfo.Team.ID.String(),
	}).Debug("Started resuming sandbox")

	var envdAccessToken *string = nil
	if snap.EnvSecure {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			zap.L().Error("Secure envd access token error", zap.Error(tokenErr.Err), logger.WithTemplateID(build.EnvID), logger.WithBuildID(build.ID.String()))
			a.sendAPIStoreError(c, tokenErr.Code, tokenErr.ClientMsg)

			return
		}

		envdAccessToken = &accessToken
	}

	if isOriginalSbxRunning && !originalSbx.IsExpired() {
		originalSandboxTimeout := time.Until(originalSbx.EndTime)

		_, createErr := a.startSandbox(
			ctx,
			snap.SandboxID,
			originalSandboxTimeout,
			nil,
			snap.Metadata,
			alias,
			teamInfo,
			build,
			&c.Request.Header,
			true,
			nodeID,
			snap.BaseEnvID,
			autoPause,
			envdAccessToken,
			snap.AllowInternetAccess,
		)

		if createErr != nil {
			zap.L().Error("Failed to resume original cloned sandbox", zap.Error(createErr.Err))
			a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

			return
		}
	}

	clonedSandboxID := InstanceIDPrefix + id.Generate()

	clonedSbx, createErr := a.startSandbox(
		ctx,
		clonedSandboxID,
		timeout,
		nil,
		snap.Metadata,
		alias,
		teamInfo,
		build,
		&c.Request.Header,
		true,
		nodeID,
		snap.BaseEnvID,
		autoPause,
		envdAccessToken,
		snap.AllowInternetAccess,
	)

	if createErr != nil {
		zap.L().Error("Failed to clone sandbox", zap.Error(createErr.Err))
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, &clonedSbx)
}
