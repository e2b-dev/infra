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
	typesteam "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDResume(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.ReportEvent(ctx, "Parsed body")

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDResumeJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err), err)

		return
	}

	timeout := sandbox.SandboxTimeoutDefault
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours), nil)

			return
		}
	}

	sandboxID = utils.ShortID(sandboxID)

	telemetry.SetAttributesWithGin(c, ctx,
		telemetry.WithSandboxID(sandboxID),
	)

	sandboxData, err := a.orchestrator.GetSandbox(ctx, sandboxID)
	if err == nil {
		if sandboxData.TeamID != teamInfo.Team.ID {
			a.sendAPIStoreError(c, ctx, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID), nil)

			return
		}

		switch sandboxData.State {
		case sandbox.StatePausing:
			logger.L().Debug(ctx, "Waiting for sandbox to pause", logger.WithSandboxID(sandboxID))
			err = a.orchestrator.WaitForStateChange(ctx, sandboxID)
			if err != nil {
				a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error waiting for sandbox to pause", err)

				return
			}
		case sandbox.StateKilling:
			a.sendAPIStoreError(c, ctx, http.StatusNotFound, "Sandbox can't be resumed, no snapshot found", nil)

			return
		case sandbox.StateRunning:
			a.sendAPIStoreError(c, ctx, http.StatusConflict, fmt.Sprintf("Sandbox %s is already running", sandboxID), nil)

			logger.L().Debug(ctx, "Sandbox is already running",
				logger.WithSandboxID(sandboxID),
				zap.Time("end_time", sandboxData.EndTime),
				zap.Time("start_time", sandboxData.StartTime),
				zap.String("node_id", sandboxData.NodeID),
			)

			return
		default:
			logger.L().Error(ctx, "Sandbox is in an unknown state", logger.WithSandboxID(sandboxID), zap.String("state", string(sandboxData.State)))
			a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Sandbox is in an unknown state", nil)

			return
		}
	}

	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.sendAPIStoreError(c, ctx, http.StatusNotFound, "Sandbox can't be resumed, no snapshot found", nil)

			return
		}

		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error when getting snapshot", err)

		return
	}

	if lastSnapshot.Snapshot.TeamID != teamInfo.Team.ID {
		err = fmt.Errorf("snapshot for sandbox '%s' belongs to team '%s', expected team '%s'", sandboxID, lastSnapshot.Snapshot.TeamID.String(), teamInfo.Team.ID.String())
		a.sendAPIStoreError(c, ctx, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID), err)

		return
	}

	autoPause := lastSnapshot.Snapshot.AutoPause
	if body.AutoPause != nil {
		autoPause = *body.AutoPause
	}
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
	}).Debug(ctx, "Started resuming sandbox")

	var envdAccessToken *string = nil
	if snap.EnvSecure {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			a.sendAPIStoreError(c, ctx, tokenErr.Code, tokenErr.ClientMsg, tokenErr.Err)

			return
		}

		envdAccessToken = &accessToken
	}

	var network *types.SandboxNetworkConfig
	if snap.Config != nil {
		network = snap.Config.Network
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
		network,
		nil, // mcp
	)
	if createErr != nil {
		a.sendAPIStoreError(c, ctx, createErr.Code, createErr.ClientMsg, createErr.Err)

		return
	}

	c.JSON(http.StatusCreated, &sbx)
}
