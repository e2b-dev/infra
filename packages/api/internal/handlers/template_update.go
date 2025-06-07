package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envalias"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PatchTemplatesTemplateID serves to update a template
func (a *APIStore) PatchTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.TemplateUpdateRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))

		return
	}

	cleanedAliasOrEnvID, err := id.CleanEnvID(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid env ID: %s", aliasOrTemplateID))

		telemetry.ReportCriticalError(ctx, "invalid env ID", err)

		return
	}

	// Prepare info for updating env
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting default team", err)

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
		).Only(ctx)

	notFound := models.IsNotFound(err)
	if notFound {
		telemetry.ReportError(ctx, "template not found", fmt.Errorf("template '%s' not found", aliasOrTemplateID))
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("the sandbox template '%s' wasn't found", cleanedAliasOrEnvID))

		return
	} else if err != nil {
		telemetry.ReportError(ctx, "failed to get env", err, attribute.String("env_id", aliasOrTemplateID))

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting env")

		return
	}

	var team *queries.Team
	for _, t := range teams {
		if t.Team.ID == template.TeamID {
			team = &t.Team
			break
		}
	}

	if team == nil {
		telemetry.ReportError(ctx, "user doesn't have access to the sandbox template", fmt.Errorf("user '%s' doesn't have access to the sandbox template '%s'", userID, cleanedAliasOrEnvID))

		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You (%s) don't have access to sandbox template '%s'", userID, cleanedAliasOrEnvID))

		return
	}

	if body.Public != nil {
		// Update env
		dbErr := a.db.UpdateEnv(ctx, template.ID, db.UpdateEnvInput{
			Public: *body.Public,
		})

		if dbErr != nil {
			telemetry.ReportError(ctx, "error when updating env", dbErr)

			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when updating env")
			return
		}
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		attribute.String("env.id", template.ID),
	)

	a.templateCache.Invalidate(template.ID)

	telemetry.ReportEvent(ctx, "updated env")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "updated environment", properties.Set("environment", template.ID))

	zap.L().Info("Updated env", zap.String("env_id", template.ID), zap.String("team_id", team.ID.String()))

	c.JSON(http.StatusOK, nil)
}
