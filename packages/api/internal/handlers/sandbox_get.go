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

	info, err := a.orchestrator.GetInstance(ctx, sandboxId)
	if err != nil {
		c.JSON(http.StatusNotFound, fmt.Sprintf("instance \"%s\" doesn't exist or you don't have access to it", id))
		return
	}

	if *info.TeamID != team.ID {
		c.JSON(http.StatusNotFound, fmt.Sprintf("instance \"%s\" doesn't exist or you don't have access to it", id))
		return
	}

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "get running instance", properties)

	build, err := a.db.Client.EnvBuild.Query().Where(envbuild.ID(*info.BuildID)).First(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, err)
		c.JSON(http.StatusInternalServerError, fmt.Sprintf("Error getting build for instance %s", id))

		return
	}

	memoryMB := int32(-1)
	cpuCount := int32(-1)

	if build != nil {
		memoryMB = int32(build.RAMMB)
		cpuCount = int32(build.Vcpu)
	}

	instance := api.RunningSandbox{
		ClientID:   info.Instance.ClientID,
		TemplateID: info.Instance.TemplateID,
		Alias:      info.Instance.Alias,
		SandboxID:  info.Instance.SandboxID,
		StartedAt:  info.StartTime,
		CpuCount:   cpuCount,
		MemoryMB:   memoryMB,
		EndAt:      info.GetEndTime(),
	}

	if info.Metadata != nil {
		meta := api.SandboxMetadata(info.Metadata)
		instance.Metadata = &meta
	}

	c.JSON(http.StatusOK, instance)
}
