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

	telemetry.ReportEvent(ctx, "get running instance")

	sandboxId := strings.Split(id, "-")[0]

	// Try to get the running instance first
	info, err := a.orchestrator.GetInstance(ctx, sandboxId)
	if err == nil && *info.TeamID == team.ID {
		// Instance exists and belongs to the team - return running sandbox info
		build, err := a.db.Client.EnvBuild.Query().Where(envbuild.ID(*info.BuildID)).First(ctx)
		if err != nil {
			telemetry.ReportCriticalError(ctx, err)
			c.JSON(http.StatusInternalServerError, fmt.Sprintf("Error getting build for instance %s", id))
			return
		}

		instance := api.RunningSandbox{
			ClientID:   info.Instance.ClientID,
			TemplateID: info.Instance.TemplateID,
			Alias:      info.Instance.Alias,
			SandboxID:  info.Instance.SandboxID,
			StartedAt:  info.StartTime,
			CpuCount:   int32(build.Vcpu),
			MemoryMB:   int32(build.RAMMB),
			EndAt:      info.EndTime,
			State:      "running",
		}

		if info.Metadata != nil {
			meta := api.SandboxMetadata(info.Metadata)
			instance.Metadata = &meta
		}

		c.JSON(http.StatusOK, instance)
		return
	}

	// If instance not found or doesn't belong to team, try to get the latest snapshot
	snapshot, build, envAliases, err := a.db.GetLastSnapshot(ctx, sandboxId, team.ID)
	if err != nil {
		fmt.Println(err)
		c.JSON(http.StatusNotFound, fmt.Sprintf("instance or snapshot \"%s\" doesn't exist or you don't have access to it", id))
		return
	}

	// optional
	var alias *string
	if envAliases != nil && len(envAliases) > 0 {
		alias = &envAliases[0].ID
	}

	instance := api.RunningSandbox{
		ClientID:   "",
		TemplateID: snapshot.EnvID,
		Alias:      alias,
		SandboxID:  snapshot.SandboxID,
		StartedAt:  snapshot.SandboxStartedAt,
		CpuCount:   int32(build.Vcpu),
		MemoryMB:   int32(build.RAMMB),
		EndAt:      snapshot.PausedAt,
		State:      "paused",
	}

	if snapshot.Metadata != nil {
		meta := api.SandboxMetadata(snapshot.Metadata)
		instance.Metadata = &meta
	}

	c.JSON(http.StatusOK, instance)
}
