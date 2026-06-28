package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// deleteTemplateAndSoftDeleteBuilds soft-deletes the template's exclusive build
// layers (status='deleted', preserving the env_builds rows and their
// team/env attribution for later storage GC) and then deletes the template, in
// a single transaction. Returns the alias cache keys captured before cascade
// deletion. Build artifacts are intentionally NOT removed from storage here.
func (a *APIStore) deleteTemplateAndSoftDeleteBuilds(ctx context.Context, teamID uuid.UUID, templateID, reason string) ([]string, error) {
	txDB, tx, err := a.sqlcDB.WithTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Must run before DeleteTemplate: the exclusivity check reads
	// env_build_assignments, which DeleteTemplate cascades away.
	if err := txDB.MarkExclusiveTemplateBuildsDeleted(ctx, queries.MarkExclusiveTemplateBuildsDeletedParams{
		TemplateID: templateID,
		Reason:     dbtypes.BuildReason{Message: reason},
	}); err != nil {
		return nil, fmt.Errorf("mark builds deleted: %w", err)
	}

	aliasKeys, err := txDB.DeleteTemplate(ctx, queries.DeleteTemplateParams{
		TemplateID: templateID,
		TeamID:     teamID,
	})
	if err != nil {
		return nil, fmt.Errorf("delete template: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return aliasKeys, nil
}

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

	// Soft-delete the template's exclusive build layers and delete the template
	// (cascades to env_build_assignments, env_aliases, snapshot_templates).
	// Returns alias cache keys captured before cascade deletion for cache invalidation.
	// Build artifacts are intentionally NOT deleted from storage here because builds are layered diffs
	// that may be referenced by other builds' header mappings.
	// [ENG-3477] a future GC mechanism will handle orphaned storage.
	aliasKeys, err := a.deleteTemplateAndSoftDeleteBuilds(ctx, team.ID, templateID, "Template deleted by user")
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting template from db", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting template")

		return
	}

	a.templateCache.InvalidateAllTags(context.WithoutCancel(ctx), templateID)
	a.templateCache.InvalidateAliasesByTemplateID(context.WithoutCancel(ctx), templateID, aliasKeys)

	telemetry.ReportEvent(ctx, "deleted template from db")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "deleted environment", properties.Set("environment", templateID))

	logger.L().Info(ctx, "Deleted template", logger.WithTemplateID(templateID), logger.WithTeamID(team.ID.String()))

	c.Status(http.StatusNoContent)
}
