package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) getVolume(c *gin.Context, volumeID string) (queries.Volume, *types.Team, bool) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeam(ctx, c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return queries.Volume{}, team, false
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
	)

	volumeIDuuid, err := uuid.Parse(volumeID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid volume ID")
		telemetry.ReportCriticalError(ctx, "error when parsing volume ID", err)

		return queries.Volume{}, team, false
	}

	volume, err := a.sqlcDB.GetVolume(ctx, queries.GetVolumeParams{
		VolumeID: volumeIDuuid,
		TeamID:   team.ID,
	})

	switch {
	case dberrors.IsNotFoundError(err):
		a.sendAPIStoreError(c, http.StatusNotFound, "Volume not found")
		telemetry.ReportError(ctx, "volume not found", err)

		return volume, team, false
	case err != nil:
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting volume")
		telemetry.ReportCriticalError(ctx, "error when getting volume", err)

		return volume, team, false
	default:
		return volume, team, true
	}
}
