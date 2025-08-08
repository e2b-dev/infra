package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// Deprecated: (07-2025) Used only temporarily during migration phase to take client ID part from sandbox ID instead of from snapshot database row.
func getSandboxIDClient(sandboxID string) (string, bool) {
	parts := strings.Split(sandboxID, "-")
	if len(parts) != 2 {
		return "", false
	}

	return parts[1], true
}

func (a *APIStore) PostSandboxesSandboxIDResume(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.ReportEvent(ctx, "Parsed body")

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDResumeJSONRequestBody](ctx, c)
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

	autoPause := instance.InstanceAutoPauseDefault
	if body.AutoPause != nil {
		autoPause = *body.AutoPause
	}

	sandboxID = utils.ShortID(sandboxID)

	// This is also checked during in orchestrator.CreateSandbox, where the sandbox ID is reserved,
	// but we want to do a quick check here to return an error quickly if possible.
	sbxCache, err := a.orchestrator.GetSandbox(sandboxID)
	if err == nil {
		zap.L().Debug("Sandbox is already running",
			logger.WithSandboxID(sandboxID),
			zap.Time("end_time", sbxCache.GetEndTime()),
			zap.Bool("auto_pause", sbxCache.AutoPause.Load()),
			zap.Time("start_time", sbxCache.StartTime),
			zap.String("node_id", sbxCache.Node.NodeID),
		)
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox %s is already running", sandboxID))

		return
	}

	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{SandboxID: sandboxID, TeamID: teamInfo.Team.ID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			zap.L().Debug("Snapshot not found", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox snapshot not found")
			return
		}

		zap.L().Error("Error getting last snapshot", logger.WithSandboxID(sandboxID), zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting snapshot")
		return
	}

	snap := lastSnapshot.Snapshot
	build := lastSnapshot.EnvBuild

	var nodeID *string
	if snap.OriginNodeID != nil {
		nodeID = snap.OriginNodeID
	} else {
		// TODO: After migration period, we can remove this part, because all actively used snapshots will be stored in the database with the node ID.
		// https://linear.app/e2b/issue/E2B-2662/remove-taking-client-from-sandbox-during-resume
		sbxClientID, ok := getSandboxIDClient(sandboxID)
		if ok {
			nodeID = &sbxClientID
		}
	}

	// Wait for any pausing for this sandbox in progress.
	pausedOnNode, err := a.orchestrator.WaitForPause(ctx, sandboxID)
	if err != nil && !errors.Is(err, instance.ErrPausingInstanceNotFound) {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error while pausing sandbox %s: %s", sandboxID, err))

		return
	}

	if err == nil {
		// If the pausing was in progress, prefer to restore on the node where the pausing happened.
		nodeID = &pausedOnNode.ID
	}

	alias := ""
	if len(lastSnapshot.Aliases) > 0 {
		alias = lastSnapshot.Aliases[0]
	}

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: *build.EnvID,
		TeamID:     teamInfo.Team.ID.String(),
	}).Debug("Started resuming sandbox")

	var envdAccessToken *string = nil
	if snap.EnvSecure {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			zap.L().Error("Secure envd access token error", zap.Error(tokenErr.Err), logger.WithTemplateID(*build.EnvID), logger.WithBuildID(build.ID.String()))
			a.sendAPIStoreError(c, tokenErr.Code, tokenErr.ClientMsg)
			return
		}

		envdAccessToken = &accessToken
	}

	sbx, createErr := a.startSandbox(
		ctx,
		snap.SandboxID,
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
		zap.L().Error("Failed to resume sandbox", zap.Error(createErr.Err))
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, &sbx)
}
