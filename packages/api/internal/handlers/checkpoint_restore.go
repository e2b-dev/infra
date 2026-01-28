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
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	typesteam "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDCheckpointsCheckpointIDRestore(c *gin.Context, sandboxID api.SandboxID, checkpointID api.CheckpointID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sandboxID = utils.ShortID(sandboxID)

	checkpoint, err := a.sqlcDB.GetCheckpoint(ctx, queries.GetCheckpointParams{
		CheckpointID: checkpointID,
		SandboxID:    sandboxID,
		TeamID:       teamID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Checkpoint '%s' not found for sandbox '%s'", checkpointID.String(), sandboxID))
			return
		}
		logger.L().Error(ctx, "Error getting checkpoint", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error restoring checkpoint")
		return
	}

	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Sandbox '%s' has no snapshot", sandboxID))
			return
		}
		logger.L().Error(ctx, "Error getting last snapshot", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error restoring checkpoint")
		return
	}

	sbx, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err == nil && sbx.State == sandbox.StateRunning {
		telemetry.ReportEvent(ctx, "Killing running sandbox for restore")
		err = a.orchestrator.RemoveSandbox(ctx, sbx, sandbox.StateActionKill)
		if err != nil && !errors.Is(err, orchestrator.ErrSandboxNotFound) {
			logger.L().Error(ctx, "Error killing sandbox for restore", zap.Error(err), logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error restoring checkpoint")
			return
		}
	}

	build := queries.EnvBuild{
		ID:                 checkpoint.CheckpointID,
		EnvID:              checkpoint.TemplateID,
		Vcpu:               checkpoint.Vcpu,
		RamMb:              checkpoint.RamMb,
		TotalDiskSizeMb:    checkpoint.TotalDiskSizeMb,
		KernelVersion:      checkpoint.KernelVersion,
		FirecrackerVersion: checkpoint.FirecrackerVersion,
		EnvdVersion:        checkpoint.EnvdVersion,
		ClusterNodeID:      checkpoint.ClusterNodeID,
	}

	snap := lastSnapshot.Snapshot
	nodeID := checkpoint.ClusterNodeID

	alias := ""
	if len(lastSnapshot.Aliases) > 0 {
		alias = lastSnapshot.Aliases[0]
	}

	var envdAccessToken *string = nil
	if snap.EnvSecure {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			logger.L().Error(ctx, "Secure envd access token error", zap.Error(tokenErr.Err), logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, tokenErr.Code, tokenErr.ClientMsg)
			return
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
	if createErr != nil {
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)
		return
	}

	c.Status(http.StatusNoContent)
}
