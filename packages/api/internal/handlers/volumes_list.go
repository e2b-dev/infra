package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetVolumes(c *gin.Context) {
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

	result, err := a.sqlcDB.FindVolumesByTeamID(ctx, team.ID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting volumes")
		telemetry.ReportCriticalError(ctx, "error when getting volumes", err)

		return
	}

	volumes := make([]api.Volume, len(result))
	for i, v := range result {
		volumes[i] = api.Volume{
			Id:   v.ID.String(),
			Name: v.Name,
		}
	}

	c.JSON(http.StatusOK, volumes)
}
