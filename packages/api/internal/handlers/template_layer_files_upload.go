package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetTemplatesTemplateIDFilesHash(c *gin.Context, templateID api.TemplateID, hash string) {
	ctx := c.Request.Context()

	// Check if the user has access to the template
	templateDB, err := a.sqlcDB.GetTemplateByID(ctx, templateID)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err), err)

		return
	}

	dbTeamID := templateDB.TeamID.String()
	team, apiErr := a.GetTeam(ctx, c, &dbTeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	// Check if the user has access to the template
	if team.ID != templateDB.TeamID {
		a.sendAPIStoreError(c, ctx, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err), err)

		return
	}

	node, err := a.templateManager.GetAvailableBuildClient(ctx, utils.WithClusterFallback(templateDB.ClusterID))
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusServiceUnavailable, "Error when getting available build client", err)

		return
	}

	resp, err := a.templateManager.InitLayerFileUpload(ctx, utils.WithClusterFallback(templateDB.ClusterID), node.NodeID, team.ID, templateID, hash)
	if err != nil {
		ctx = telemetry.SetAttributes(ctx, telemetry.WithTemplateID(templateID), attribute.String("hash", hash))

		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error when requesting layer files upload", err)

		return
	}

	c.JSON(http.StatusCreated, &api.TemplateBuildFileUpload{
		Present: resp.GetPresent(),
		Url:     resp.Url,
	})
}
