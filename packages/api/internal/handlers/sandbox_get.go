package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxID(c *gin.Context, id string) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	telemetry.ReportEvent(ctx, "get sandbox")

	sandboxId := utils.ShortID(id)

	var sbxDomain *string
	if team.ClusterID != nil {
		cluster, ok := a.clustersPool.GetClusterById(*team.ClusterID)
		if !ok {
			telemetry.ReportCriticalError(ctx, fmt.Sprintf("cluster with ID '%s' not found", *team.ClusterID), nil)
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("cluster with id %s not found", *team.ClusterID))

			return
		}

		sbxDomain = cluster.SandboxDomain
	}

	// Try to get the running sandbox first
	sbx, err := a.orchestrator.GetSandbox(sandboxId, true)
	if err == nil {
		// Check if sandbox belongs to the team
		if sbx.TeamID != team.ID {
			telemetry.ReportCriticalError(ctx, fmt.Sprintf("sandbox '%s' doesn't belong to team '%s'", sandboxId, team.ID.String()), nil)
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))

			return
		}

		state := api.Running
		switch sbx.State {
		// Sandbox is being paused or already is paused, user can work with that as if it's paused
		case sandbox.StatePausing:
			state = api.Paused
		// Sandbox is being stopped or already is stopped, user can't work with it anymore
		case sandbox.StateKilling:
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))

			return
		}

		// Sandbox exists and belongs to the team - return running sandbox sbx
		sandbox := api.SandboxDetail{
			ClientID:        sbx.ClientID,
			TemplateID:      sbx.TemplateID,
			Alias:           sbx.Alias,
			SandboxID:       sbx.SandboxID,
			StartedAt:       sbx.StartTime,
			CpuCount:        api.CPUCount(sbx.VCpu),
			MemoryMB:        api.MemoryMB(sbx.RamMB),
			DiskSizeMB:      api.DiskSizeMB(sbx.TotalDiskSizeMB),
			EndAt:           sbx.EndTime,
			State:           state,
			EnvdVersion:     sbx.EnvdVersion,
			EnvdAccessToken: sbx.EnvdAccessToken,
			Domain:          sbxDomain,
		}

		if sbx.Metadata != nil {
			meta := api.SandboxMetadata(sbx.Metadata)
			sandbox.Metadata = &meta
		}

		c.JSON(http.StatusOK, sandbox)
		return
	}

	// If sandbox not found try to get the latest snapshot
	lastSnapshot, err := a.sqlcDB.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{SandboxID: sandboxId, TeamID: team.ID})
	if err != nil {
		telemetry.ReportError(ctx, "error getting last snapshot", err)
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))

		return
	}

	memoryMB := int32(lastSnapshot.EnvBuild.RamMb)
	cpuCount := int32(lastSnapshot.EnvBuild.Vcpu)

	diskSize := int32(0)
	if lastSnapshot.EnvBuild.TotalDiskSizeMb != nil {
		diskSize = int32(*lastSnapshot.EnvBuild.TotalDiskSizeMb)
	} else {
		zap.L().Error("disk size is not set for the sandbox", logger.WithSandboxID(id))
	}

	// This shouldn't happen - if yes, the data are in corrupted state,
	// still adding fallback to envd version v1.0.0 (should behave as if there are no features)
	envdVersion := "v1.0.0"
	if lastSnapshot.EnvBuild.EnvdVersion != nil {
		envdVersion = *lastSnapshot.EnvBuild.EnvdVersion
	} else {
		zap.L().Error("envd version is not set for the sandbox", logger.WithSandboxID(id))
	}

	var sbxAccessToken *string = nil
	if lastSnapshot.Snapshot.EnvSecure {
		key, err := a.envdAccessTokenGenerator.GenerateAccessToken(lastSnapshot.Snapshot.SandboxID)
		if err != nil {
			telemetry.ReportError(ctx, "error generating sandbox access token", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("error generating sandbox access token: %s", err))

			return
		}

		sbxAccessToken = &key
	}

	sandbox := api.SandboxDetail{
		ClientID:        consts.ClientID, // for backwards compatibility we need to return a client id
		TemplateID:      lastSnapshot.Snapshot.EnvID,
		SandboxID:       lastSnapshot.Snapshot.SandboxID,
		StartedAt:       lastSnapshot.Snapshot.SandboxStartedAt.Time,
		CpuCount:        cpuCount,
		MemoryMB:        memoryMB,
		DiskSizeMB:      diskSize,
		EndAt:           lastSnapshot.Snapshot.CreatedAt.Time, // Snapshot is created when sandbox is paused
		State:           api.Paused,
		EnvdVersion:     envdVersion,
		EnvdAccessToken: sbxAccessToken,
		Domain:          nil,
	}

	if lastSnapshot.Snapshot.Metadata != nil {
		metadata := api.SandboxMetadata(lastSnapshot.Snapshot.Metadata)
		sandbox.Metadata = &metadata
	}

	c.JSON(http.StatusOK, sandbox)
}
