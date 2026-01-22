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
)

// PatchTemplatesTemplateID serves to update a template
func (a *APIStore) PatchTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.TemplateUpdateRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))

		return
	}

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

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(aliasInfo.TemplateID),
	)

	// Update template
	if body.Public != nil {
		_, err := a.sqlcDB.UpdateTemplate(ctx, queries.UpdateTemplateParams{
			TemplateIDOrAlias: aliasInfo.TemplateID,
			TeamID:            team.ID,
			Public:            *body.Public,
		})
		if err != nil {
			if dberrors.IsNotFoundError(err) {
				a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found or you don't have access to it", aliasOrTemplateID))
				telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(aliasInfo.TemplateID))

				return
			}

			telemetry.ReportError(ctx, "error when updating template", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error updating template")

			return
		}
	}

	a.templateCache.InvalidateAllTags(aliasInfo.TemplateID)

	telemetry.ReportEvent(ctx, "updated template")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "updated environment", properties.Set("environment", aliasInfo.TemplateID))

	logger.L().Info(ctx, "Updated template", logger.WithTemplateID(aliasInfo.TemplateID), logger.WithTeamID(team.ID.String()))

	c.JSON(http.StatusOK, nil)
}
