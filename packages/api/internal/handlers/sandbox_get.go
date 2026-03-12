package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxID(c *gin.Context, id string) {
	ctx := c.Request.Context()

	teamInfo := auth.MustGetTeamInfo(c)
	team := teamInfo.Team

	telemetry.ReportEvent(ctx, "get sandbox")

	sandboxId, err := utils.ShortID(id)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	var sbxDomain *string
	if team.ClusterID != nil {
		cluster, ok := a.clusters.GetClusterById(*team.ClusterID)
		if !ok {
			telemetry.ReportCriticalError(ctx, fmt.Sprintf("cluster with ID '%s' not found", *team.ClusterID), nil)
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("cluster with id %s not found", *team.ClusterID))

			return
		}

		sbxDomain = cluster.SandboxDomain
	}

	// Try to get the running sandbox first
	sbx, err := a.orchestrator.GetSandbox(ctx, team.ID, sandboxId)
	if err == nil {
		// Check if sandbox belongs to the team
		if sbx.TeamID != team.ID {
			telemetry.ReportCriticalError(ctx, fmt.Sprintf("sandbox '%s' doesn't belong to team '%s'", sandboxId, team.ID.String()), nil)
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

			return
		}

		state := api.Running
		switch sbx.State {
		// Sandbox is being paused or already is paused, user can work with that as if it's paused
		case sandbox.StatePausing:
			state = api.Paused
		// Sandbox is being stopped or already is stopped, user can't work with it anymore
		case sandbox.StateKilling:
			logger.L().Debug(ctx, "Sandbox is being killed", logger.WithSandboxID(sandboxId))
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

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
			VolumeMounts:    convertFromDBMountsToAPIMounts(sbx.VolumeMounts),
		}

		if sbx.Metadata != nil {
			meta := api.SandboxMetadata(sbx.Metadata)
			sandbox.Metadata = &meta
		}

		c.JSON(http.StatusOK, sandbox)

		return
	}

	// If sandbox not found try to get the latest snapshot
	// TODO: ENG-3544 scope GetLastSnapshot query by teamID to avoid post-fetch ownership check.
	lastSnapshot, err := a.snapshotCache.Get(ctx, sandboxId)
	if err != nil {
		if errors.Is(err, snapshotcache.ErrSnapshotNotFound) {
			telemetry.ReportError(ctx, "snapshot not found", err, telemetry.WithSandboxID(sandboxId))
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

			return
		}

		telemetry.ReportCriticalError(ctx, "error getting last snapshot", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting sandbox")

		return
	}

	if lastSnapshot.Snapshot.TeamID != team.ID {
		telemetry.ReportError(ctx, fmt.Sprintf("snapshot for sandbox '%s' doesn't belong to team '%s'", sandboxId, team.ID.String()), nil)
		a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

		return
	}

	memoryMB := int32(lastSnapshot.EnvBuild.RamMb)
	cpuCount := int32(lastSnapshot.EnvBuild.Vcpu)

	diskSize := int32(0)
	if lastSnapshot.EnvBuild.TotalDiskSizeMb != nil {
		diskSize = int32(*lastSnapshot.EnvBuild.TotalDiskSizeMb)
	} else {
		logger.L().Error(ctx, "disk size is not set for the sandbox", logger.WithSandboxID(id))
	}

	// This shouldn't happen - if yes, the data are in corrupted state,
	// still adding fallback to envd version v1.0.0 (should behave as if there are no features)
	envdVersion := "v1.0.0"
	if lastSnapshot.EnvBuild.EnvdVersion != nil {
		envdVersion = *lastSnapshot.EnvBuild.EnvdVersion
	} else {
		logger.L().Error(ctx, "envd version is not set for the sandbox", logger.WithSandboxID(id))
	}

	var sbxAccessToken *string = nil
	if lastSnapshot.Snapshot.EnvSecure {
		key, err := a.accessTokenGenerator.GenerateEnvdAccessToken(lastSnapshot.Snapshot.SandboxID)
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
		EndAt:           lastSnapshot.EnvBuild.CreatedAt, // Latest build created_at represents last pause time
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
