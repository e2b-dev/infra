package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostVolumes(c *gin.Context) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeam(ctx, c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
	)

	body, err := utils.ParseBody[api.PostVolumesJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	telemetry.ReportEvent(ctx, "Parsed body")

	volume, err := a.sqlcDB.CreateVolume(ctx, queries.CreateVolumeParams{
		TeamID: team.ID,
		Name:   body.Name,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when creating volume")
		telemetry.ReportCriticalError(ctx, "error when creating volume", err)

		return
	}

	result := api.Volume{
		Id:   volume.ID.String(),
		Name: volume.Name,
	}

	c.JSON(http.StatusCreated, result)
}
