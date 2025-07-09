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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxID(c *gin.Context, id string) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	telemetry.ReportEvent(ctx, "get sandbox")

	sandboxId := strings.Split(id, "-")[0]

	// Try to get the running sandbox first
	info, err := a.orchestrator.GetInstance(ctx, sandboxId)
	if err == nil {
		// Check if sandbox belongs to the team
		if *info.TeamID != team.ID {
			zap.L().Warn("sandbox doesn't exist or you don't have access to it", logger.WithSandboxID(id))
			c.JSON(http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))
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
			EndAt:           info.GetEndTime(),
			State:           api.Running,
			EnvdVersion:     &info.EnvdVersion,
			EnvdAccessToken: info.EnvdAccessToken,
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
		zap.L().Warn("error getting last snapshot for sandbox", logger.WithSandboxID(id), zap.Error(err))
		c.JSON(http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))
		return
	}

	memoryMB := int32(lastSnapshot.EnvBuild.RamMb)
	cpuCount := int32(lastSnapshot.EnvBuild.Vcpu)

	var sbxAccessToken *string = nil
	if lastSnapshot.Snapshot.EnvSecure {
		key, err := a.envdAccessTokenGenerator.GenerateAccessToken(lastSnapshot.Snapshot.SandboxID)
		if err != nil {
			zap.L().Error("error generating sandbox access token", logger.WithSandboxID(id), zap.Error(err))
			c.JSON(http.StatusInternalServerError, fmt.Sprintf("error generating sandbox access token: %s", err))
			return
		}

		sbxAccessToken = &key
	}

	sandbox := api.SandboxDetail{
		ClientID:        "00000000", // for backwards compatibility we need to return a client id
		TemplateID:      lastSnapshot.Snapshot.EnvID,
		SandboxID:       lastSnapshot.Snapshot.SandboxID,
		StartedAt:       lastSnapshot.Snapshot.SandboxStartedAt.Time,
		CpuCount:        cpuCount,
		MemoryMB:        memoryMB,
		EndAt:           info.GetEndTime(),
		State:           api.Paused,
		EnvdVersion:     lastSnapshot.EnvBuild.EnvdVersion,
		EnvdAccessToken: sbxAccessToken,
	}

	if lastSnapshot.Snapshot.Metadata != nil {
		metadata := api.SandboxMetadata(lastSnapshot.Snapshot.Metadata)
		sandbox.Metadata = &metadata
	}

	c.JSON(http.StatusOK, sandbox)
}
