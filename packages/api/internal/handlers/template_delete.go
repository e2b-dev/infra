package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
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

	// Check if there are running sandboxes that use this template as their base
	sandboxes, err := a.orchestrator.GetSandboxes(ctx, team.ID, []sandbox.State{sandbox.StateRunning, sandbox.StatePausing, sandbox.StateSnapshotting})
	if err != nil {
		telemetry.ReportError(ctx, "error when checking for running sandboxes", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when checking for running sandboxes")

		return
	}

	for _, sbx := range sandboxes {
		if sbx.BaseTemplateID == templateID {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("cannot delete template '%s' because there are running sandboxes using it", templateID))

			return
		}
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

	// Delete the template from DB (cascades to env_build_assignments, env_aliases, snapshot_templates).
	// Returns alias cache keys and active builds captured before the cascade delete.
	// Build artifacts are intentionally NOT deleted from storage here because builds are layered diffs
	// that may be referenced by other builds' header mappings.
	// [ENG-3477] a future GC mechanism will handle orphaned storage.
	deleteRows, err := a.sqlcDB.DeleteTemplate(ctx, queries.DeleteTemplateParams{
		TemplateID: templateID,
		TeamID:     team.ID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting template from db", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting template")

		return
	}

	// Split results into alias keys (for cache invalidation) and active builds (for cancellation).
	var aliasKeys []string
	var activeBuilds []queries.DeleteTemplateRow

	for _, row := range deleteRows {
		if row.AliasKey != "" {
			aliasKeys = append(aliasKeys, row.AliasKey)
		}

		if row.BuildID != nil {
			activeBuilds = append(activeBuilds, row)
		}
	}

	a.templateCache.InvalidateAllTags(context.WithoutCancel(ctx), templateID)
	a.templateCache.InvalidateAliasesByTemplateID(context.WithoutCancel(ctx), templateID, aliasKeys)

	// Cancel any active builds that were running for this template.
	a.cancelActiveBuilds(context.WithoutCancel(ctx), templateID, activeBuilds)

	telemetry.ReportEvent(ctx, "deleted template from db")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "deleted environment", properties.Set("environment", templateID))

	logger.L().Info(ctx, "Deleted template", logger.WithTemplateID(templateID), logger.WithTeamID(team.ID.String()))

	c.Status(http.StatusNoContent)
}

// cancelActiveBuilds stops in-progress builds on the orchestrator.
func (a *APIStore) cancelActiveBuilds(ctx context.Context, templateID string, builds []queries.DeleteTemplateRow) {
	if len(builds) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ctx, span := tracer.Start(ctx, "cancel active-builds")
	defer span.End()

	for _, b := range builds {
		clusterID := clusters.WithClusterFallback(b.ClusterID)

		// Stop the build on the orchestrator node if it's running.
		if b.ClusterNodeID != nil {
			deleteErr := a.templateManager.DeleteBuild(ctx, *b.BuildID, templateID, clusterID, *b.ClusterNodeID)
			if deleteErr != nil {
				logger.L().Error(ctx, "Failed to cancel build on node during template deletion",
					zap.String("buildID", b.BuildID.String()),
					logger.WithTemplateID(templateID),
					zap.Error(deleteErr))
			}
		}
	}

	logger.L().Info(ctx, "Cancelled active builds after template deletion", zap.Int("count", len(builds)))
}
