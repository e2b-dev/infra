package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
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

const maxSnapshotsPerTeam = 100

func (a *APIStore) PostSandboxesSandboxIDSnapshots(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := a.GetTeamInfo(c)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	sandboxID = utils.ShortID(sandboxID)

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
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' is not running or is already being snapshotted", sandboxID))

		return
	}

	snapshotCount, err := a.sqlcDB.CountTeamSnapshots(ctx, teamID)
	if err != nil {
		logger.L().Error(ctx, "Error counting team snapshots", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot")

		return
	}

	if snapshotCount >= maxSnapshotsPerTeam {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("Snapshot limit reached. Maximum %d snapshots per team", maxSnapshotsPerTeam))

		return
	}

	telemetry.ReportEvent(ctx, "Creating snapshot")

	err = a.orchestrator.RemoveSandbox(ctx, sbx, sandbox.StateActionPause)
	if err != nil {
		if errors.Is(err, orchestrator.ErrSandboxNotFound) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Sandbox '%s' not found", sandboxID))

			return
		}
		telemetry.ReportError(ctx, "Error pausing sandbox for snapshot", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot")

		return
	}

	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		logger.L().Error(ctx, "Error getting snapshot after pause", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.tryResumeSandbox(ctx, sandboxID, teamInfo, &c.Request.Header)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot")

		return
	}

	snapshotID := "snapshot_" + id.Generate()
	build := lastSnapshot.EnvBuild
	snap := lastSnapshot.Snapshot

	snapshotResult, err := a.sqlcDB.CreateSnapshotTemplate(ctx, queries.CreateSnapshotTemplateParams{
		SnapshotID:      snapshotID,
		CreatedBy:       nil,
		TeamID:          teamID,
		SourceSandboxID: &sandboxID,
		BaseTemplateID:  &snap.BaseEnvID,
		ExistingBuildID: build.ID,
	})
	if err != nil {
		logger.L().Error(ctx, "Error creating snapshot template", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.tryResumeSandbox(ctx, sandboxID, teamInfo, &c.Request.Header)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot")

		return
	}

	snapshotInfo := buildSnapshotInfo(
		snapshotResult.SnapshotID,
		&sandboxID,
		&snap.BaseEnvID,
		lastSnapshot.EnvBuild.CreatedAt,
		build.Vcpu,
		build.RamMb,
		build.TotalDiskSizeMb,
	)

	resumeErr := a.resumeSandboxAfterSnapshot(ctx, sandboxID, teamInfo, lastSnapshot, &c.Request.Header)
	if resumeErr != nil {
		logger.L().Error(ctx, "Error resuming sandbox after snapshot", zap.Error(resumeErr.Err), logger.WithSandboxID(sandboxID))
		telemetry.ReportError(ctx, "Snapshot created but failed to resume sandbox", resumeErr.Err)
		c.JSON(http.StatusCreated, snapshotInfo)

		return
	}

	c.JSON(http.StatusCreated, snapshotInfo)
}

func (a *APIStore) resumeSandboxAfterSnapshot(
	ctx context.Context,
	sandboxID string,
	teamInfo *typesteam.Team,
	lastSnapshot queries.GetLastSnapshotRow,
	requestHeader *http.Header,
) *api.APIError {
	snap := lastSnapshot.Snapshot
	build := lastSnapshot.EnvBuild
	nodeID := &snap.OriginNodeID

	alias := ""
	if len(lastSnapshot.Aliases) > 0 {
		alias = lastSnapshot.Aliases[0]
	}

	var envdAccessToken *string
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
		requestHeader,
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

func (a *APIStore) tryResumeSandbox(ctx context.Context, sandboxID string, teamInfo *typesteam.Team, requestHeader *http.Header) {
	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		logger.L().Error(ctx, "Error getting snapshot for recovery resume", zap.Error(err), logger.WithSandboxID(sandboxID))

		return
	}

	resumeErr := a.resumeSandboxAfterSnapshot(ctx, sandboxID, teamInfo, lastSnapshot, requestHeader)
	if resumeErr != nil {
		logger.L().Error(ctx, "Error during recovery resume of sandbox", zap.Error(resumeErr.Err), logger.WithSandboxID(sandboxID))
	}
}
