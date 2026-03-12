package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetVolumesVolumeID(c *gin.Context, volumeID api.VolumeID) {
	volume, team, ok := a.getVolume(c, volumeID)
	if !ok {
		return
	}

	token, err := generateVolumeContentToken(a.config.VolumesToken, volume, team)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to sign token")
		telemetry.ReportCriticalError(c.Request.Context(), "failed to sign token", err)

		return
	}

	result := api.VolumeAndToken{
		VolumeID: volume.ID.String(),
		Name:     volume.Name,
		Token:    token,
	}

	c.JSON(http.StatusOK, result)
}
