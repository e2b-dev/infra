package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// DeleteTemplatesTemplateID serves to delete a template (e.g. in CLI)
func (a *APIStore) DeleteTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	ctx := c.Request.Context()

	cleanedAliasOrTemplateID, _, err := id.ParseTemplateIDOrAliasWithTag(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", aliasOrTemplateID))

		telemetry.ReportCriticalError(ctx, "invalid template ID", err)

		return
	}

	builds, err := a.sqlcDB.GetExclusiveBuildsForTemplateDeletion(ctx, cleanedAliasOrTemplateID)
	if err != nil {
		telemetry.ReportError(ctx, "failed to get template", fmt.Errorf("failed to get template: %w", err), telemetry.WithTemplateID(aliasOrTemplateID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")

		return
	}

	if len(builds) == 0 {
		telemetry.ReportError(ctx, "template not found", nil, telemetry.WithTemplateID(aliasOrTemplateID))
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found or you don't have access to it", aliasOrTemplateID))

		return
	}

	templateID := builds[0].Env.ID
	teamUUID := builds[0].Env.TeamID
	teamID := teamUUID.String()
	team, apiErr := a.GetTeam(ctx, c, &teamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(templateID),
	)

	if team.ID != teamUUID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", aliasOrTemplateID))
		telemetry.ReportError(ctx, "no access to the template", nil, telemetry.WithTemplateID(templateID))

		return
	}

	// check if base template has snapshots
	hasSnapshots, err := a.sqlcDB.ExistsTemplateSnapshots(ctx, templateID)
	if err != nil {
		telemetry.ReportError(ctx, "error when checking if base template has snapshots", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when checking if template has snapshots")

		return
	}

	if hasSnapshots {
		telemetry.ReportError(ctx, "base template has paused sandboxes", nil, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("cannot delete template '%s' because there are paused sandboxes using it", templateID))

		return
	}

	err = a.sqlcDB.DeleteTemplate(ctx, queries.DeleteTemplateParams{
		TemplateID: templateID,
		TeamID:     team.ID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting template from db", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting template")

		return
	}

	// get all build ids
	buildIds := make([]template_manager.DeleteBuild, 0)
	for _, build := range builds {
		// Skip if there was no build
		if build.ClusterNodeID == nil {
			continue
		}

		buildIds = append(buildIds, template_manager.DeleteBuild{
			BuildID:    build.BuildID,
			TemplateID: build.Env.ID,
			ClusterID:  utils.WithClusterFallback(team.ClusterID),
			NodeID:     *build.ClusterNodeID,
		})
	}

	// delete all builds
	err = a.templateManager.DeleteBuilds(ctx, buildIds)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting template files from storage", err)
	} else {
		telemetry.ReportEvent(ctx, "deleted template from storage")
	}

	a.templateCache.InvalidateAllTags(templateID)

	telemetry.ReportEvent(ctx, "deleted template from db")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "deleted environment", properties.Set("environment", templateID))

	logger.L().Info(ctx, "Deleted template", logger.WithTemplateID(templateID), logger.WithTeamID(team.ID.String()))

	c.JSON(http.StatusOK, nil)
}
