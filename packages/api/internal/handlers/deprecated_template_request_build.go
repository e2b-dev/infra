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
	"github.com/e2b-dev/infra/packages/db/dberrors"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
)

func (a *APIStore) PostTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

	userID := a.GetUserID(c)

	body, err := utils.ParseBody[api.TemplateBuildRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err), err)

		return
	}

	team, apiErr := a.GetTeam(ctx, c, body.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	telemetry.ReportEvent(ctx, "started creating new environment")

	templateID := id.Generate()
	span.SetAttributes(telemetry.WithTemplateID(templateID))

	template, apiErr := a.buildTemplate(ctx, userID, team, templateID, body)
	if apiErr != nil {
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(ctx, userID.String(), team.ID.String(), "submitted environment build request", properties.
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
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err), err)

		return
	}

	templateID, _, err := id.ParseTemplateIDOrAliasWithTag(rawTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", rawTemplateID), err)

		return
	}
	span.SetAttributes(telemetry.WithTemplateID(templateID))

	team, apiErr := a.GetTeam(ctx, c, body.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	templateDB, err := a.sqlcDB.GetTemplateByID(ctx, templateID)
	ctx = telemetry.SetAttributes(ctx, telemetry.WithTemplateID(templateID))

	switch {
	case err == nil:
		if templateDB.TeamID != team.ID {
			a.sendAPIStoreError(c, ctx, http.StatusForbidden, fmt.Sprintf("You do not have access to the template '%s'", templateID), err)

			return
		}
	case dberrors.IsNotFoundError(err):
		a.sendAPIStoreError(c, ctx, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", templateID), err)

		return
	default:
		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, fmt.Sprintf("Error when getting template: %s", err), err)

		return
	}

	template, apiErr := a.buildTemplate(ctx, userID, team, templateID, body)
	if apiErr != nil {
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(ctx, userID.String(), team.ID.String(), "submitted environment build request", properties.
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
	firecrackerVersion := a.featureFlags.StringFlag(ctx, featureflags.BuildFirecrackerVersion)

	var alias *string
	var tags []string

	if body.Alias != nil {
		var err error
		a, t, err := id.ParseTemplateIDOrAliasWithTag(*body.Alias)
		if err != nil {
			return nil, &api.APIError{
				Code:      http.StatusBadRequest,
				ClientMsg: fmt.Sprintf("Invalid alias: %s", err),
				Err:       err,
			}
		}

		alias = &a
		if t != nil {
			tags, err = id.ValidateAndDeduplicateTags([]string{*t})
			if err != nil {
				return nil, &api.APIError{
					Code:      http.StatusBadRequest,
					ClientMsg: fmt.Sprintf("Invalid tag: %s", err),
					Err:       err,
				}
			}
		}
	}

	// Create the build
	data := template.RegisterBuildData{
		ClusterID:          utils.WithClusterFallback(team.ClusterID),
		TemplateID:         templateID,
		UserID:             &userID,
		Team:               team,
		Dockerfile:         body.Dockerfile,
		Alias:              alias,
		Tags:               tags,
		StartCmd:           body.StartCmd,
		ReadyCmd:           body.ReadyCmd,
		CpuCount:           body.CpuCount,
		MemoryMB:           body.MemoryMB,
		Version:            templates.TemplateV1Version,
		KernelVersion:      a.config.DefaultKernelVersion,
		FirecrackerVersion: firecrackerVersion,
	}

	return template.RegisterBuild(ctx, a.templateBuildsCache, a.sqlcDB, data)
}
