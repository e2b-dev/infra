package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (a *APIStore) GetTemplatesTemplateIDFilesHash(c *gin.Context, templateID api.TemplateID, hash string) {
	ctx := c.Request.Context()

	_, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting default team", err)

		return
	}

	teamIDs := utils.Map(teams, func(t queries.GetTeamsWithUsersTeamsWithTierRow) uuid.UUID {
		return t.Team.ID
	})

	// Check if the user has access to the template
	_, err = a.db.Client.Env.Query().Where(env.ID(templateID), env.TeamIDIn(teamIDs...)).Only(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when getting template", err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err))
		return
	}

	resp, err := a.templateManager.InitLayerFileUpload(ctx, templateID, hash)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when requesting layer files upload", err, telemetry.WithTemplateID(templateID), attribute.String("hash", hash))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when requesting layer files upload")
		return
	}

	c.JSON(http.StatusCreated, &api.TemplateBuildFileUpload{
		Present: resp.Present,
		Url:     resp.Url,
	})
}
