package handlers

import (
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const gracePeriod = time.Minute

func (a *APIStore) PostVolumesVolumeIDToken(c *gin.Context, volumeID api.VolumeID) {
	volume, team, ok := a.getVolume(c, volumeID)
	if !ok {
		return
	}

	clusterID := clusters.WithClusterFallback(team.ClusterID)

	now := time.Now()
	notBefore := now.Add(-1 * gracePeriod)
	expiration := now.Add(a.config.VolumesToken.Expiration)

	claims := jwt.MapClaims{
		// registered
		"aud": clusterID.String(),
		"exp": jwt.NewNumericDate(expiration),
		"iat": jwt.NewNumericDate(now),
		"iss": a.config.VolumesToken.Issuer,
		"jti": uuid.NewString(),
		"nbf": jwt.NewNumericDate(notBefore),
		"sub": team.ID.String(),

		// custom
		"teamid":  team.ID,
		"volid":   volume.ID.String(),
		"voltype": volume.VolumeType,
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
