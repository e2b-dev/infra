package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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
		telemetry.ReportError(ctx, "failed to get env", err, telemetry.WithTemplateID(aliasOrTemplateID))

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting env")

		return
	}

	dbTeamID := template.TeamID.String()
	team, apiErr := a.GetTeamAndLimits(c, &dbTeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)
		return
	}

	if team.ID != template.TeamID {
		telemetry.ReportError(ctx, "access to the sandbox template denied", fmt.Errorf("access to the sandbox template '%s' denied", cleanedAliasOrEnvID))

		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", cleanedAliasOrEnvID))

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
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	a.templateCache.Invalidate(template.ID)

	telemetry.ReportEvent(ctx, "updated env")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "updated environment", properties.Set("environment", template.ID))

	zap.L().Info("Updated env", logger.WithTemplateID(template.ID), logger.WithTeamID(team.ID.String()))

	c.JSON(http.StatusOK, nil)
}
