package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) GetVolumesVolumeID(c *gin.Context, volumeID api.VolumeID) {
	volume, _, ok := a.getVolume(c, volumeID)
	if !ok {
		return
	}

	result := api.Volume{
		Id:   volume.ID.String(),
		Name: volume.Name,
	}

	c.JSON(http.StatusOK, result)
}
