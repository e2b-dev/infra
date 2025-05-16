package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
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

		err = fmt.Errorf("invalid env ID: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	// Prepare info for deleting env
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		err = fmt.Errorf("error when getting default team: %w", err)
		telemetry.ReportCriticalError(ctx, err)

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
		telemetry.ReportError(ctx, fmt.Errorf("template '%s' not found", aliasOrTemplateID))
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("the sandbox template '%s' wasn't found", cleanedAliasOrEnvID))

		return
	} else if err != nil {
		telemetry.ReportError(ctx, fmt.Errorf("failed to get env '%s': %w", aliasOrTemplateID, err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting env")

		return
	}

	var team *models.Team
	for _, t := range teams {
		if t.ID == template.TeamID {
			team = t
			break
		}
	}

	if team == nil {
		errMsg := fmt.Errorf("user '%s' doesn't have access to the sandbox template '%s'", userID, cleanedAliasOrEnvID)
		telemetry.ReportError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You (%s) don't have access to sandbox template '%s'", userID, cleanedAliasOrEnvID))

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		attribute.String("env.id", template.ID),
	)

	// check if base env has snapshots
	hasSnapshots, err := a.db.CheckBaseEnvHasSnapshots(ctx, template.ID)
	if err != nil {
		telemetry.ReportError(ctx, fmt.Errorf("error when checking if base env has snapshots: %w", err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when checking if base env has snapshots")

		return
	}

	if hasSnapshots {
		telemetry.ReportError(ctx, fmt.Errorf("base template '%s' has paused sandboxes", template.ID))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("cannot delete template '%s' because there are paused sandboxes using it", template.ID))

		return
	}

	dbErr := a.db.DeleteEnv(ctx, template.ID)
	if dbErr != nil {
		errMsg := fmt.Errorf("error when deleting env from db: %w", dbErr)
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting env")

		return
	}

	// get all build ids
	buildIds := make([]uuid.UUID, len(template.Edges.Builds))
	for i, build := range template.Edges.Builds {
		buildIds[i] = build.ID
	}

	// delete all builds
	deleteJobErr := a.templateManager.DeleteBuilds(ctx, template.ID, buildIds)
	if deleteJobErr != nil {
		errMsg := fmt.Errorf("error when deleting env files from storage: %w", deleteJobErr)
		telemetry.ReportCriticalError(ctx, errMsg)
	} else {
		telemetry.ReportEvent(ctx, "deleted env from storage")
	}

	a.templateCache.Invalidate(template.ID)

	telemetry.ReportEvent(ctx, "deleted env from db")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "deleted environment", properties.Set("environment", template.ID))

	a.logger.Infof("Deleted env '%s' from team '%s'", template.ID, team.ID)

	c.JSON(http.StatusOK, nil)
}
