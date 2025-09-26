package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/template"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostV2Templates triggers a new template build
func (a *APIStore) PostV2Templates(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := apiutils.ParseBody[api.TemplateBuildRequestV2](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return
	}

	telemetry.ReportEvent(ctx, "started environment build")

	// Prepare info for rebuilding env
	team, tier, apiErr := a.GetTeamAndTier(c, body.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)
		return
	}

	// Create the build, find the template ID by alias or generate a new one
	_, span := tracer.Start(ctx, "find-template-alias")
	defer span.End()
	templateID := id.Generate()
	isNew := true
	templateAlias, err := a.sqlcDB.GetTemplateAliasByAlias(ctx, body.Alias)
	if err != nil {
		var notFoundErr db.NotFoundError
		if !errors.As(err, &notFoundErr) {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting template alias: %s", err))
			telemetry.ReportCriticalError(ctx, "error when getting template alias", err)
			return
		}
	} else {
		templateID = templateAlias.EnvID
		isNew = false
	}
	span.End()

	builderNodeID, err := a.templateManager.GetAvailableBuildClient(ctx, apiutils.WithClusterFallback(team.ClusterID))
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting available build client")
		telemetry.ReportCriticalError(ctx, "error when getting available build client", err, telemetry.WithTemplateID(templateID))
		return
	}

	buildReq := template.RegisterBuildData{
		ClusterID:     apiutils.WithClusterFallback(team.ClusterID),
		BuilderNodeID: builderNodeID,
		IsNew:         isNew,
		TemplateID:    templateID,
		UserID:        nil,
		Team:          team,
		Tier:          tier,
		Alias:         &body.Alias,
		CpuCount:      body.CpuCount,
		MemoryMB:      body.MemoryMB,
	}

	template, apiError := template.RegisterBuild(ctx, a.templateBuildsCache, a.db, a.sqlcDB, buildReq)
	if apiError != nil {
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)
		telemetry.ReportCriticalError(ctx, "invalid request body", err)
		return
	}

	_, span = tracer.Start(ctx, "posthog-analytics")
	defer span.End()
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", body.Alias),
	)
	span.End()

	c.JSON(http.StatusAccepted, &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Public:     template.Public,
		Aliases:    template.Aliases,
	})
}
