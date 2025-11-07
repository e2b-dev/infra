package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
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
	team := c.Value(auth.TeamContextKey).(*types.Team)

	body, err := utils.ParseBody[api.TemplateUpdateRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))

		return
	}

	// No data passed
	if body.Public == nil {
		c.Status(http.StatusOK)

		return
	}

	cleanedAliasOrTemplateID, err := id.CleanTemplateID(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", aliasOrTemplateID))

		telemetry.ReportCriticalError(ctx, "invalid template ID", err)

		return
	}

	// Update template
	templateID, err := a.sqlcDB.UpdateTemplate(ctx, queries.UpdateTemplateParams{
		TemplateIDOrAlias: cleanedAliasOrTemplateID,
		TeamID:            team.ID,
		Public:            *body.Public,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found or you don't have access to it", aliasOrTemplateID))
			telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(aliasOrTemplateID))

			return
		}

		telemetry.ReportError(ctx, "error when updating template", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error updating template")

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(templateID),
	)

	a.templateCache.Invalidate(templateID)

	telemetry.ReportEvent(ctx, "updated template")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "updated environment", properties.Set("environment", templateID))

	zap.L().Info("Updated template", logger.WithTemplateID(templateID), logger.WithTeamID(team.ID.String()))

	c.JSON(http.StatusOK, nil)
}
