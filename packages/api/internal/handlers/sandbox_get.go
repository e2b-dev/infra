package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
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

	sandboxId := strings.Split(id, "-")[0]

	var sbxDomain *string
	if team.ClusterID != nil {
		cluster, ok := a.clustersPool.GetClusterById(*team.ClusterID)
		if !ok {
			telemetry.ReportCriticalError(ctx, "error getting cluster by ID", fmt.Errorf("cluster with ID '%s' not found", *team.ClusterID))
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("cluster with id %s not found", *team.ClusterID))

			return
		}

		sbxDomain = cluster.SandboxDomain
	}

	// Try to get the running sandbox first
	info, err := a.orchestrator.GetInstance(ctx, sandboxId)
	if err == nil {
		// Check if sandbox belongs to the team
		if info.TeamID != team.ID {
			telemetry.ReportCriticalError(ctx, "sandbox doesn't belong to team", fmt.Errorf("sandbox '%s' doesn't belong to team '%s'", sandboxId, team.ID.String()))
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))

			return
		}

		// Sandbox exists and belongs to the team - return running sandbox info
		sandbox := api.SandboxDetail{
			ClientID:        info.Instance.ClientID,
			TemplateID:      info.Instance.TemplateID,
			Alias:           info.Instance.Alias,
			SandboxID:       info.Instance.SandboxID,
			StartedAt:       info.StartTime,
			CpuCount:        api.CPUCount(info.VCpu),
			MemoryMB:        api.MemoryMB(info.RamMB),
			DiskSizeMB:      api.DiskSizeMB(info.TotalDiskSizeMB),
			EndAt:           info.GetEndTime(),
			State:           api.Running,
			EnvdVersion:     info.EnvdVersion,
			EnvdAccessToken: info.EnvdAccessToken,
			Domain:          sbxDomain,
		}

		if info.Metadata != nil {
			meta := api.SandboxMetadata(info.Metadata)
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
