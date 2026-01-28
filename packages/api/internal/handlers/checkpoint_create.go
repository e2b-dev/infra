package handlers

import (
	"context"
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
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	maxCheckpointsPerSandbox = 10
	checkpointTagPrefix      = "chk_"
)

func (a *APIStore) PostSandboxesSandboxIDCheckpoints(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sandboxID = utils.ShortID(sandboxID)

	body, err := utils.ParseBody[api.NewCheckpoint](ctx, c)
	if err != nil {
		body = api.NewCheckpoint{}
	}

	sbx, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		if errors.Is(err, orchestrator.ErrSandboxNotFound) {
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

	if sbx.State != sandbox.StateRunning {
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' is not running", sandboxID))
		return
	}

	count, err := a.sqlcDB.CountCheckpoints(ctx, queries.CountCheckpointsParams{
		SandboxID: sandboxID,
		TeamID:    teamID,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		logger.L().Error(ctx, "Error counting checkpoints", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error counting checkpoints")
		return
	}

	if count >= maxCheckpointsPerSandbox {
		a.sendAPIStoreError(c, http.StatusTooManyRequests, fmt.Sprintf("Maximum number of checkpoints (%d) reached for sandbox '%s'", maxCheckpointsPerSandbox, sandboxID))
		return
	}

	checkpointTag := checkpointTagPrefix + id.Generate()[:8]
	if body.Name != nil && *body.Name != "" {
		checkpointTag = checkpointTagPrefix + *body.Name + "_" + id.Generate()[:8]
	}

	telemetry.ReportEvent(ctx, "Creating checkpoint")

	err = a.orchestrator.RemoveSandbox(ctx, sbx, sandbox.StateActionPause)
	if err != nil {
		if errors.Is(err, orchestrator.ErrSandboxNotFound) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Sandbox '%s' not found", sandboxID))
			return
		}
		telemetry.ReportError(ctx, "Error pausing sandbox for checkpoint", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating checkpoint")
		return
	}

	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		logger.L().Error(ctx, "Error getting snapshot after pause", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating checkpoint")
		return
	}

	err = a.sqlcDB.CreateTemplateBuildAssignment(ctx, queries.CreateTemplateBuildAssignmentParams{
		TemplateID: lastSnapshot.Snapshot.EnvID,
		BuildID:    lastSnapshot.EnvBuild.ID,
		Tag:        checkpointTag,
	})
	if err != nil {
		logger.L().Error(ctx, "Error creating checkpoint assignment", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating checkpoint")
		return
	}

	checkpointCreatedAt := time.Now()

	resumeErr := a.resumeSandboxAfterCheckpoint(ctx, sandboxID, teamInfo, lastSnapshot)
	if resumeErr != nil {
		logger.L().Error(ctx, "Error resuming sandbox after checkpoint", zap.Error(resumeErr.Err), logger.WithSandboxID(sandboxID))
	}

	checkpointName := checkpointTag
	if body.Name != nil && *body.Name != "" {
		checkpointName = *body.Name
	}

	c.JSON(http.StatusCreated, &api.CheckpointInfo{
		CheckpointID: lastSnapshot.EnvBuild.ID,
		SandboxID:    sandboxID,
		Name:         &checkpointName,
		CreatedAt:    checkpointCreatedAt,
	})
}

func (a *APIStore) resumeSandboxAfterCheckpoint(
	ctx context.Context,
	sandboxID string,
	teamInfo *typesteam.Team,
	lastSnapshot queries.GetLastSnapshotRow,
) *api.APIError {
	snap := lastSnapshot.Snapshot
	build := lastSnapshot.EnvBuild
	nodeID := &snap.OriginNodeID

	alias := ""
	if len(lastSnapshot.Aliases) > 0 {
		alias = lastSnapshot.Aliases[0]
	}

	var envdAccessToken *string = nil
	if snap.EnvSecure {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			return tokenErr
		}
		envdAccessToken = &accessToken
	}

	var network *types.SandboxNetworkConfig
	if snap.Config != nil {
		network = snap.Config.Network
	}

	timeout := sandbox.SandboxTimeoutDefault

	_, createErr := a.startSandbox(
		ctx,
		snap.SandboxID,
		timeout,
		nil,
		snap.Metadata,
		alias,
		teamInfo,
		build,
		nil,
		true,
		nodeID,
		snap.BaseEnvID,
		snap.AutoPause,
		envdAccessToken,
		snap.AllowInternetAccess,
		network,
		nil,
	)

	return createErr
}
