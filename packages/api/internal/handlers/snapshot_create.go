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

func (a *APIStore) PostSandboxesSandboxIDSnapshots(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sandboxID = utils.ShortID(sandboxID)

	body, err := utils.ParseBody[api.NewSnapshot](ctx, c)
	if err != nil {
		body = api.NewSnapshot{}
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
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot")
		return
	}

	snapshotID := "snapshot_" + id.Generate()
	build := lastSnapshot.EnvBuild
	snap := lastSnapshot.Snapshot

	var metadata map[string]string
	if body.Metadata != nil {
		metadata = *body.Metadata
	}
	_ = metadata

	snapshotResult, err := a.sqlcDB.CreateSnapshotTemplate(ctx, queries.CreateSnapshotTemplateParams{
		SnapshotID:         snapshotID,
		CreatedBy:          nil,
		TeamID:             teamID,
		SourceSandboxID:    &sandboxID,
		Vcpu:               build.Vcpu,
		RamMb:              build.RamMb,
		FreeDiskSizeMb:     build.FreeDiskSizeMb,
		KernelVersion:      build.KernelVersion,
		FirecrackerVersion: build.FirecrackerVersion,
		EnvdVersion:        build.EnvdVersion,
		Status:             "success",
		OriginNodeID:       build.ClusterNodeID,
		TotalDiskSizeMb:    build.TotalDiskSizeMb,
		CpuArchitecture:    build.CpuArchitecture,
		CpuFamily:          build.CpuFamily,
		CpuModel:           build.CpuModel,
		CpuModelName:       build.CpuModelName,
		CpuFlags:           build.CpuFlags,
	})
	if err != nil {
		logger.L().Error(ctx, "Error creating snapshot template", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot")
		return
	}

	resumeErr := a.resumeSandboxAfterSnapshot(ctx, sandboxID, teamInfo, lastSnapshot)
	if resumeErr != nil {
		logger.L().Error(ctx, "Error resuming sandbox after snapshot", zap.Error(resumeErr.Err), logger.WithSandboxID(sandboxID))
	}

	cpuCount := api.CPUCount(int32(build.Vcpu))
	memoryMB := api.MemoryMB(int32(build.RamMb))
	var diskSizeMB *api.DiskSizeMB
	if build.TotalDiskSizeMb != nil {
		d := api.DiskSizeMB(int32(*build.TotalDiskSizeMb))
		diskSizeMB = &d
	}

	c.JSON(http.StatusCreated, &api.SnapshotInfo{
		SnapshotID:  snapshotResult.SnapshotID,
		SandboxID:   &sandboxID,
		TemplateID:  &snap.BaseEnvID,
		CreatedAt:   lastSnapshot.EnvBuild.CreatedAt,
		CpuCount:    &cpuCount,
		MemoryMB:    &memoryMB,
		DiskSizeMB:  diskSizeMB,
	})
}

func (a *APIStore) resumeSandboxAfterSnapshot(
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
