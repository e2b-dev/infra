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
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDConnect(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := auth.MustGetTeamInfo(c)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.ReportEvent(ctx, "Parsed body")

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDConnectJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	timeout := time.Duration(body.Timeout) * time.Second
	if timeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

		return
	}

	teamID := teamInfo.Team.ID

	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	// It could happen that after sandbox transition, it'll be again transitioning, retry up to maxConnectRetries times.
	const maxConnectRetries = 3

	for attempt := range maxConnectRetries {
		sbx, apiErr := a.orchestrator.KeepAliveFor(ctx, teamID, sandboxID, timeout, false)
		if apiErr == nil {
			c.JSON(http.StatusOK, sbx.ToAPISandbox())

			return
		}

		// Sandbox not in store at all → fall through to snapshot resume.
		var notFoundErr *sandbox.NotFoundError
		if errors.As(apiErr.Err, &notFoundErr) {
			break
		}

		// Sandbox exists but isn't running → check which transitional state.
		var notRunningErr *sandbox.NotRunningError
		if !errors.As(apiErr.Err, &notRunningErr) {
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}

		if notRunningErr.State == sandbox.StateKilling {
			a.sendAPIStoreError(c, http.StatusConflict, utils.SandboxChangingStateMsg(sandboxID, notRunningErr.State))

			return
		}

		logger.L().Info(ctx, "Sandbox not running, waiting for state change",
			logger.WithSandboxID(sandboxID),
			zap.String("state", string(notRunningErr.State)),
			zap.Int("attempt", attempt+1),
		)

		err = a.orchestrator.WaitForStateChange(ctx, teamID, sandboxID)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError,
				"Error waiting for sandbox state change")

			return
		}

		continue
	}

	// TODO: ENG-3544 scope GetLastSnapshot query by teamID to avoid post-fetch ownership check.
	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			logger.L().Debug(ctx, "Snapshot not found", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))

			return
		}

		logger.L().Error(ctx, "Error getting last snapshot", logger.WithSandboxID(sandboxID), zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting snapshot")

		return
	}

	if lastSnapshot.Snapshot.TeamID != teamID {
		telemetry.ReportError(ctx, fmt.Sprintf("snapshot for sandbox '%s' doesn't belong to team '%s'", sandboxID, teamID.String()), nil)
		a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))

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
		TemplateID: snap.EnvID,
		TeamID:     teamID.String(),
	}).Debug(ctx, "Started resuming sandbox")

	var envdAccessToken *string = nil
	if snap.EnvSecure {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			logger.L().Error(ctx, "Secure envd access token error", zap.Error(tokenErr.Err), logger.WithTemplateID(snap.EnvID), logger.WithBuildID(build.ID.String()), logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, tokenErr.Code, tokenErr.ClientMsg)

			return
		}

		envdAccessToken = &accessToken
	}

	var network *types.SandboxNetworkConfig
	if snap.Config != nil {
		network = snap.Config.Network
	}
	var autoResume *types.SandboxAutoResumeConfig
	if snap.Config != nil {
		autoResume = snap.Config.AutoResume
	}

	var volumes []*types.SandboxVolumeMountConfig
	if snap.Config != nil {
		volumes = snap.Config.VolumeMounts
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
		snap.EnvID,
		snap.BaseEnvID,
		autoPause,
		autoResume,
		envdAccessToken,
		snap.AllowInternetAccess,
		network,
		nil, // mcp
		convertDatabaseMountsToOrchestratorMounts(volumes), // volumes
	)
	if createErr != nil {
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, &sbx)
}
