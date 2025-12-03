package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// PatchTemplatesTemplateID serves to update a template
func (a *APIStore) PatchTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.TemplateUpdateRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))

		return
	}

	cleanedAliasOrTemplateID, err := id.CleanTemplateID(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", aliasOrTemplateID))

		telemetry.ReportCriticalError(ctx, "invalid template ID", err)

		return
	}

	template, err := a.sqlcDB.GetTemplateByIdOrAlias(ctx, cleanedAliasOrTemplateID)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", aliasOrTemplateID))
			telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(aliasOrTemplateID))

			return
		}

		telemetry.ReportError(ctx, "error when getting template", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting template")

		return
	}

	team, apiErr := a.GetTeam(ctx, c, sharedUtils.ToPtr(template.TeamID.String()))
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	if template.TeamID != team.ID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", aliasOrTemplateID))
		telemetry.ReportError(ctx, "no access to the template", nil, telemetry.WithTemplateID(template.ID))

		return
	}

	// Update template
	if body.Public != nil {
		_, err := a.sqlcDB.UpdateTemplate(ctx, queries.UpdateTemplateParams{
			TemplateIDOrAlias: cleanedAliasOrTemplateID,
			TeamID:            team.ID,
			Public:            *body.Public,
		})
		if err != nil {
			if dberrors.IsNotFoundError(err) {
				a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found or you don't have access to it", aliasOrTemplateID))
				telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(template.ID))

				return
			}

			telemetry.ReportError(ctx, "error when updating template", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error updating template")

			return
		}
	}

	a.templateCache.Invalidate(template.ID)

	telemetry.ReportEvent(ctx, "updated template")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "updated environment", properties.Set("environment", template.ID))

	logger.L().Info(ctx, "Updated template", logger.WithTemplateID(template.ID), logger.WithTeamID(team.ID.String()))

	c.JSON(http.StatusOK, nil)
}
