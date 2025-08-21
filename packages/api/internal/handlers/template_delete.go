package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envalias"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// DeleteTemplatesTemplateID serves to delete an env (e.g. in CLI)
func (a *APIStore) DeleteTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	ctx := c.Request.Context()

	cleanedAliasOrEnvID, err := id.CleanEnvID(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid env ID: %s", aliasOrTemplateID))

		telemetry.ReportCriticalError(ctx, "invalid env ID", err)

		return
	}

	template, err := a.db.
		Client.
		Env.
		Query().
		Where(
			env.Or(
				env.HasEnvAliasesWith(envalias.ID(aliasOrTemplateID)),
				env.ID(aliasOrTemplateID),
			),
		).
		WithBuilds().
		Only(ctx)

	notFound := models.IsNotFound(err)
	if notFound {
		telemetry.ReportError(ctx, "template not found", nil, telemetry.WithTemplateID(aliasOrTemplateID))
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("the sandbox template '%s' wasn't found", cleanedAliasOrEnvID))

		return
	} else if err != nil {
		telemetry.ReportError(ctx, "failed to get template", fmt.Errorf("failed to get template: %w", err), telemetry.WithTemplateID(aliasOrTemplateID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")

		return
	}

	dbTeamID := template.TeamID.String()
	team, _, apiErr := a.GetTeamAndTier(c, &dbTeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)
		return
	}

	if team.ID != template.TeamID {
		a.sendAPIStoreError(c, http.StatusForbidden, "User does not have access to the template")
		telemetry.ReportCriticalError(ctx, "user does not have access to the template", nil, telemetry.WithTemplateID(template.ID))

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	// check if base env has snapshots
	hasSnapshots, err := a.db.CheckBaseEnvHasSnapshots(ctx, template.ID)
	if err != nil {
		telemetry.ReportError(ctx, "error when checking if base env has snapshots", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when checking if base env has snapshots")

		return
	}

	if hasSnapshots {
		telemetry.ReportError(ctx, "base template has paused sandboxes", nil, telemetry.WithTemplateID(template.ID))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("cannot delete template '%s' because there are paused sandboxes using it", template.ID))

		return
	}

	dbErr := a.db.DeleteEnv(ctx, template.ID)
	if dbErr != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting env from db", dbErr)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting env")

		return
	}

	// get all build ids
	buildIds := make([]template_manager.DeleteBuild, len(template.Edges.Builds))
	for i, build := range template.Edges.Builds {
		buildIds[i] = template_manager.DeleteBuild{
			BuildID:    build.ID,
			TemplateID: *build.EnvID,
		}
	}

	// delete all builds
	deleteJobErr := a.templateManager.DeleteBuilds(ctx, buildIds)
	if deleteJobErr != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting env files from storage", deleteJobErr)
	} else {
		telemetry.ReportEvent(ctx, "deleted env from storage")
	}

	a.templateCache.Invalidate(template.ID)

	telemetry.ReportEvent(ctx, "deleted env from db")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "deleted environment", properties.Set("environment", template.ID))

	zap.L().Info("Deleted env", logger.WithTemplateID(template.ID), logger.WithTeamID(team.ID.String()))

	c.JSON(http.StatusOK, nil)
}
