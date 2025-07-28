package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetTemplatesTemplateIDFilesHash(c *gin.Context, templateID api.TemplateID, hash string) {
	ctx := c.Request.Context()

	// Check if the user has access to the template
	templateDB, err := a.sqlcDB.GetTemplateByID(ctx, templateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err))
		telemetry.ReportCriticalError(ctx, "error when getting env", err, telemetry.WithTemplateID(templateID))
		return
	}

	dbTeamID := templateDB.TeamID.String()
	team, _, apiErr := a.GetTeamAndTier(c, &dbTeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)
		return
	}

	// Check if the user has access to the template
	if team.ID != templateDB.TeamID {
		telemetry.ReportCriticalError(ctx, "error when getting template", err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err))
		return
	}

	resp, err := a.templateManager.InitLayerFileUpload(ctx, team.ID, templateID, hash)
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
