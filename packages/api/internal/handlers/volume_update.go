package handlers

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PatchVolumesVolumeID(c *gin.Context, volumeID api.VolumeID) {
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

	volumeIDuuid, err := uuid.Parse(volumeID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid volume ID")
		telemetry.ReportCriticalError(ctx, "error when parsing volume ID", err)

		return
	}

	body, err := utils.ParseBody[api.PatchVolumesVolumeIDJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	telemetry.ReportEvent(ctx, "Parsed body")

	// validate body
	if !isValidVolumeName(body.Name) {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid volume name")
		telemetry.ReportError(ctx, "invalid volume name", nil)

		return
	}

	volume, err := a.sqlcDB.UpdateVolume(ctx, queries.UpdateVolumeParams{
		TeamID:   team.ID,
		VolumeID: volumeIDuuid,
		Name:     body.Name,
	})
	if err != nil {
		if dberrors.IsUniqueConstraintViolation(err) {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Volume with name '%s' already exists", body.Name))
			telemetry.ReportError(ctx, "volume already exists", err)

			return
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when updating volume")
		telemetry.ReportCriticalError(ctx, "error when updating volume", err)

		return
	}

	result := api.Volume{
		Id:   volume.ID.String(),
		Name: volume.Name,
	}

	c.JSON(http.StatusOK, result)
}

var validVolumeNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func isValidVolumeName(name string) bool {
	return validVolumeNameRegex.MatchString(name)
}
