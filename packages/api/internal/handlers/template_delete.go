package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// DeleteTemplatesTemplateID serves to delete a template (e.g. in CLI)
func (a *APIStore) DeleteTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	ctx := c.Request.Context()

	identifier, _, err := id.ParseName(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid template ID", err)

		return
	}

	// Resolve template and get the owning team
	team, aliasInfo, apiErr := a.resolveTemplateAndTeam(ctx, c, identifier)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		if apiErr.Code != http.StatusNotFound {
			telemetry.ReportCriticalError(ctx, "error resolving template", apiErr.Err)
		}

		return
	}

	templateID := aliasInfo.TemplateID

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(templateID),
	)

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

	// Delete the template from DB (cascades to env_build_assignments, env_aliases, snapshot_templates).
	// Build artifacts are intentionally NOT deleted from storage here because builds are layered diffs
	// that may be referenced by other builds' header mappings.
	// [ENG-3477] a future GC mechanism will handle orphaned storage.
	err = a.sqlcDB.DeleteTemplate(ctx, queries.DeleteTemplateParams{
		TemplateID: templateID,
		TeamID:     team.ID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting template from db", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting template")

		return
	}

	a.templateCache.InvalidateAllTags(templateID)

	telemetry.ReportEvent(ctx, "deleted template from db")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "deleted environment", properties.Set("environment", templateID))

	logger.L().Info(ctx, "Deleted template", logger.WithTemplateID(templateID), logger.WithTeamID(team.ID.String()))

	c.Status(http.StatusNoContent)
}
