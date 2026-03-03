package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const gracePeriod = time.Minute

func (a *APIStore) GetVolumesVolumeID(c *gin.Context, volumeID api.VolumeID) {
	volume, team, ok := a.getVolume(c, volumeID)
	if !ok {
		return
	}

	token, err := a.generateVolumeContentToken(volume, team)
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

func (a *APIStore) generateVolumeContentToken(volume queries.Volume, team *types.Team) (string, error) {
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
		"teamid":  team.ID.String(),
		"volid":   volume.ID.String(),
		"voltype": volume.VolumeType,
	}

	token := jwt.NewWithClaims(a.config.VolumesToken.SigningMethod, claims)
	signedToken, err := token.SignedString(a.config.VolumesToken.SigningKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return signedToken, nil
}
