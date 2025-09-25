package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/template"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	envID := id.Generate()

	telemetry.ReportEvent(ctx, "started creating new environment")

	template := a.TemplateRequestBuild(c, envID, true)
	if template != nil {
		c.JSON(http.StatusAccepted, &template)
	}
}

func (a *APIStore) PostTemplatesTemplateID(c *gin.Context, templateID api.TemplateID) {
	cleanedTemplateID, err := id.CleanEnvID(templateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", cleanedTemplateID))

		telemetry.ReportCriticalError(c.Request.Context(), "invalid template ID", err)

		return
	}

	template := a.TemplateRequestBuild(c, cleanedTemplateID, false)

	if template != nil {
		c.JSON(http.StatusAccepted, &template)
	}
}

func (a *APIStore) TemplateRequestBuild(c *gin.Context, templateID api.TemplateID, new bool) *api.Template {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.TemplateBuildRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return nil
	}

	// Prepare info for rebuilding env
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return nil
	}

	// Find the team and tier
	team, tier, err := findTeamAndTier(teams, body.TeamID)
	if err != nil {
		var statusCode int
		if body.TeamID != nil {
			statusCode = http.StatusNotFound
		} else {
			statusCode = http.StatusInternalServerError
		}

		a.sendAPIStoreError(c, statusCode, err.Error())
		telemetry.ReportCriticalError(ctx, "error finding team and tier", err)
		return nil
	}

	builderNodeID, err := a.templateManager.GetAvailableBuildClient(ctx, utils.WithClusterFallback(team.ClusterID))
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when getting available build client", err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when getting available build client")
		return nil
	}

	// Create the build
	buildReq := template.RegisterBuildData{
		ClusterID:     utils.WithClusterFallback(team.ClusterID),
		BuilderNodeID: builderNodeID,
		TemplateID:    templateID,
		IsNew:         new,
		UserID:        userID,
		Team:          team,
		Tier:          tier,
		Dockerfile:    body.Dockerfile,
		Alias:         body.Alias,
		StartCmd:      body.StartCmd,
		ReadyCmd:      body.ReadyCmd,
		CpuCount:      body.CpuCount,
		MemoryMB:      body.MemoryMB,
	}

	template, apiError := template.RegisterBuild(ctx, a.templateBuildsCache, a.db, a.sqlcDB, buildReq)
	if apiError != nil {
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)
		telemetry.ReportCriticalError(ctx, "build template request failed", err)
		return nil
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", body.Alias),
	)

	return &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Public:     template.Public,
		Aliases:    template.Aliases,
	}
}
