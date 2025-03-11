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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxID(c *gin.Context, id string) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	telemetry.ReportEvent(ctx, "get running instance")

	sandboxId := strings.Split(id, "-")[0]

	// Try to get the running sandbox first
	info, err := a.orchestrator.GetInstance(ctx, sandboxId)
	if err == nil {
		// Check if sandbox belongs to the team
		if *info.TeamID != team.ID {
			zap.L().Error("sandbox %s doesn't exist or you don't have access to it", zap.String("sandbox_id", id))
			c.JSON(http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))
			return
		}

		// Sandbox exists and belongs to the team - return running sandbox info
		_, build, err := a.templateCache.Get(ctx, info.Instance.TemplateID, team.ID, true)
		if err != nil {
			telemetry.ReportCriticalError(ctx, err.Err)
			a.sendAPIStoreError(c, err.Code, err.ClientMsg)
			return
		}

		cpuCount := int32(-1)
		memoryMB := int32(-1)

		if build != nil {
			cpuCount = int32(build.Vcpu)
			memoryMB = int32(build.RAMMB)
		}

		sandbox := api.ListedSandbox{
			ClientID:   info.Instance.ClientID,
			TemplateID: info.Instance.TemplateID,
			Alias:      info.Instance.Alias,
			SandboxID:  info.Instance.SandboxID,
			StartedAt:  info.StartTime,
			CpuCount:   cpuCount,
			MemoryMB:   memoryMB,
			EndAt:      info.GetEndTime(),
			State:      api.Running,
		}

		if info.Metadata != nil {
			meta := api.SandboxMetadata(info.Metadata)
			sandbox.Metadata = &meta
		}

		c.JSON(http.StatusOK, sandbox)
		return
	}

	// If sandbox not found try to get the latest snapshot
	snapshot, build, err := a.db.GetLastSnapshot(ctx, sandboxId, team.ID)
	if err != nil {
		zap.L().Error("error getting last snapshot for sandbox", zap.String("sandbox_id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))
		return
	}

	memoryMB := int32(-1)
	cpuCount := int32(-1)

	if build != nil {
		memoryMB = int32(build.RAMMB)
		cpuCount = int32(build.Vcpu)
	}

	sandbox := api.ListedSandbox{
		ClientID:   "00000000", // for backwards compatibility we need to return a client id
		TemplateID: snapshot.EnvID,
		SandboxID:  snapshot.SandboxID,
		StartedAt:  snapshot.SandboxStartedAt,
		CpuCount:   cpuCount,
		MemoryMB:   memoryMB,
		EndAt:      info.GetEndTime(),
		State:      api.Paused,
	}

	if snapshot.Metadata != nil {
		meta := api.SandboxMetadata(snapshot.Metadata)
		sandbox.Metadata = &meta
	}

	c.JSON(http.StatusOK, sandbox)
}
