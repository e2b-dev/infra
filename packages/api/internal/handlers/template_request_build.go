package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/template"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

	userID := a.GetUserID(c)

	body, err := utils.ParseBody[api.TemplateBuildRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)
		return
	}

	team, apiErr := a.GetTeamAndLimits(c, body.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and limits", apiErr.Err)
		return
	}

	telemetry.ReportEvent(ctx, "started creating new environment")

	templateID := id.Generate()
	span.SetAttributes(telemetry.WithTemplateID(templateID))

	template, apiErr := a.buildTemplate(ctx, userID, team, templateID, body)
	if apiErr != nil {
		telemetry.ReportCriticalError(ctx, "error when requesting template build", apiErr.Err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		return
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", body.Alias),
	)

	c.JSON(http.StatusAccepted, &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Aliases:    template.Aliases,
		Public:     false,
	})
}

func (a *APIStore) PostTemplatesTemplateID(c *gin.Context, rawTemplateID api.TemplateID) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

	userID := a.GetUserID(c)

	body, err := utils.ParseBody[api.TemplateBuildRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)
		return
	}

	templateID, err := id.CleanEnvID(rawTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", rawTemplateID))
		telemetry.ReportCriticalError(c.Request.Context(), "invalid template ID", err)
		return
	}
	span.SetAttributes(telemetry.WithTemplateID(templateID))

	team, apiErr := a.GetTeamAndLimits(c, body.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)
		return
	}

	templateDB, err := a.sqlcDB.GetTemplateByID(ctx, templateID)
	switch {
	case err == nil:
		if templateDB.TeamID != team.ID {
			a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You do not have access to the template '%s'", templateID))
			telemetry.ReportError(ctx, "template access forbidden", nil, telemetry.WithTemplateID(templateID), telemetry.WithTeamID(team.ID.String()))
			return
		}
	case dberrors.IsNotFoundError(err):
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", templateID))
		telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(templateID))
		return
	default:
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting template: %s", err))
		telemetry.ReportCriticalError(ctx, "error when getting template", err, telemetry.WithTemplateID(templateID))
		return
	}

	template, apiErr := a.buildTemplate(ctx, userID, team, templateID, body)
	if apiErr != nil {
		telemetry.ReportCriticalError(ctx, "error when requesting template build", apiErr.Err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		return
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", body.Alias),
	)

	c.JSON(http.StatusAccepted, &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Aliases:    template.Aliases,
		Public:     templateDB.Public,
	})
}

func (a *APIStore) buildTemplate(
	ctx context.Context,
	userID uuid.UUID,
	team *types.Team,
	templateID api.TemplateID,
	body api.TemplateBuildRequest,
) (*template.RegisterBuildResponse, *api.APIError) {
	builderNodeID, err := a.templateManager.GetAvailableBuildClient(ctx, utils.WithClusterFallback(team.ClusterID))
	if err != nil {
		return nil, &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: "Error when getting available build client",
			Err:       fmt.Errorf("error when getting available build client: %w", err),
		}
	}

	// Create the build
	data := template.RegisterBuildData{
		ClusterID:     utils.WithClusterFallback(team.ClusterID),
		BuilderNodeID: builderNodeID,
		TemplateID:    templateID,
		UserID:        &userID,
		Team:          team,
		Dockerfile:    body.Dockerfile,
		Alias:         body.Alias,
		StartCmd:      body.StartCmd,
		ReadyCmd:      body.ReadyCmd,
		CpuCount:      body.CpuCount,
		MemoryMB:      body.MemoryMB,
	}

	return template.RegisterBuild(ctx, a.templateBuildsCache, a.db, data)
}
