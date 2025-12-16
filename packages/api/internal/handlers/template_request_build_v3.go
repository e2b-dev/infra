package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/template"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/dberrors"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// PostV3Templates triggers a new template build
func (a *APIStore) PostV3Templates(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := apiutils.ParseBody[api.TemplateBuildRequestV3](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return
	}

	t := requestTemplateBuild(ctx, c, a, body)
	if t != nil {
		c.JSON(http.StatusAccepted, t)
	}
}

func requestTemplateBuild(ctx context.Context, c *gin.Context, a *APIStore, body api.TemplateBuildRequestV3) *api.TemplateRequestResponseV3 {
	telemetry.ReportEvent(ctx, "started environment build")

	// Prepare info for rebuilding env
	team, apiErr := a.GetTeam(ctx, c, body.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team, limits", apiErr.Err)

		return nil
	}

	alias, _, err := id.ParseTemplateIDOrAliasWithTag(body.Alias)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid alias: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid alias", err)

		return nil
	}

	// Create the build, find the template ID by alias or generate a new one
	findTemplateCtx, span := tracer.Start(ctx, "find-template-alias")
	defer span.End()
	templateID := id.Generate()
	public := false
	templateAlias, err := a.sqlcDB.GetTemplateAliasByAlias(findTemplateCtx, alias)
	switch {
	case err == nil:
		if templateAlias.TeamID != team.ID {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Alias `%s` is already taken", alias))
			telemetry.ReportError(findTemplateCtx, "template alias is already taken", nil, telemetry.WithTemplateID(templateAlias.EnvID), telemetry.WithTeamID(team.ID.String()), attribute.String("alias", alias))

			return nil
		}

		templateID = templateAlias.EnvID
		public = templateAlias.Public
	case dberrors.IsNotFoundError(err):
		// Alias is available and not used
	default:
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting template alias: %s", err))
		telemetry.ReportCriticalError(findTemplateCtx, "error when getting template alias", err)

		return nil
	}
	span.End()

	firecrackerVersion := a.featureFlags.StringFlag(ctx, featureflags.BuildFirecrackerVersion)
	tags := utils.DerefOrDefault(body.Tags, nil)

	buildReq := template.RegisterBuildData{
		ClusterID:          apiutils.WithClusterFallback(team.ClusterID),
		TemplateID:         templateID,
		UserID:             nil,
		Team:               team,
		Alias:              &alias,
		Tags:               tags,
		CpuCount:           body.CpuCount,
		MemoryMB:           body.MemoryMB,
		Version:            templates.TemplateV2LatestVersion,
		KernelVersion:      a.config.DefaultKernelVersion,
		FirecrackerVersion: firecrackerVersion,
	}

	template, apiError := template.RegisterBuild(ctx, a.templateBuildsCache, a.sqlcDB, buildReq)
	if apiError != nil {
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)
		telemetry.ReportCriticalError(ctx, "build template register failed", apiError.Err)

		return nil
	}

	posthogCtx, span := tracer.Start(ctx, "posthog-analytics")
	defer span.End()
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(posthogCtx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(posthogCtx, team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", alias).
		Set("tags", tags),
	)
	span.End()

	return &api.TemplateRequestResponseV3{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Aliases:    template.Aliases,
		Public:     public,
	}
}
