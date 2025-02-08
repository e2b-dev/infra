package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxID(c *gin.Context, id string) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	telemetry.ReportEvent(ctx, "get running sandbox")

	sandboxId := strings.Split(id, "-")[0]

	// Try to get the running sandbox first
	info, err := a.orchestrator.GetInstance(ctx, sandboxId)
	if err == nil {
		// Check if sandbox belongs to the team
		if *info.TeamID != team.ID {
			c.JSON(http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", id))
			return
		}

		// Sandbox exists and belongs to the team - return running sandbox info
		build, err := a.db.Client.EnvBuild.Query().Where(envbuild.ID(*info.BuildID)).First(ctx)
		if err != nil {
			telemetry.ReportCriticalError(ctx, err)
			c.JSON(http.StatusInternalServerError, fmt.Sprintf("Error getting build for sandbox %s", id))
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
			EndAt:      info.EndTime,
			State:      "running",
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
		fmt.Println(err)
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
		ClientID:   "00000000",
		TemplateID: snapshot.EnvID,
		SandboxID:  snapshot.SandboxID,
		StartedAt:  snapshot.SandboxStartedAt,
		CpuCount:   cpuCount,
		MemoryMB:   memoryMB,
		EndAt:      snapshot.CreatedAt,
		State:      "paused",
	}

	if snapshot.Metadata != nil {
		meta := api.SandboxMetadata(snapshot.Metadata)
		sandbox.Metadata = &meta
	}

	c.JSON(http.StatusOK, sandbox)
}
