package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostVolumesVolumeIDToken(c *gin.Context, volumeID api.VolumeID) {
	volume, team, ok := a.getVolume(c, volumeID)
	if !ok {
		return
	}

	expiration := time.Now().Add(a.config.VolumesToken.Expiration)

	claims := jwt.MapClaims{
		"iss":     a.config.VolumesToken.Issuer,
		"exp":     jwt.NewNumericDate(expiration),
		"volid":   volume.ID.String(),
		"voltype": volume.VolumeType,
		"tid":     team.ID,
	}

	token := jwt.NewWithClaims(a.config.VolumesToken.SigningMethod, claims)
	signedToken, err := token.SignedString(a.config.VolumesToken.SigningKey)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to sign token")
		telemetry.ReportCriticalError(c.Request.Context(), "failed to sign token", err)

		return
	}

	c.JSON(http.StatusOK, api.VolumeToken{Token: signedToken})
}
