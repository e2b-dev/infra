package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	feature_flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostVolumes(c *gin.Context) {
	ctx := c.Request.Context()

	// get team
	team, apiErr := a.GetTeam(ctx, c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
	)

	if !a.featureFlags.BoolFlag(ctx, feature_flags.PersistentVolumesFlag) {
		a.sendAPIStoreError(c, http.StatusBadRequest, "use of volumes is not enabled")

		return
	}

	// parse body
	body, err := utils.ParseBody[api.PostVolumesJSONRequestBody](ctx, c)
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

	ctx = feature_flags.AddToContext(ctx, feature_flags.VolumeContext(body.Name))

	volumeType := a.getVolumeType(ctx)
	if volumeType == "" {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "No persistent volume type is configured")
		telemetry.ReportCriticalError(ctx, "default persistent volume type is not configured", nil)

		return
	}

	if _, ok := a.config.PersistentVolumeTypes[volumeType]; !ok {
		a.sendAPIStoreError(c, http.StatusBadRequest, "use of volumes is not enabled")
		telemetry.ReportCriticalError(ctx, "volume type is not supported", nil)

		return
	}

	volume, err := a.sqlcDB.CreateVolume(ctx, queries.CreateVolumeParams{
		TeamID:     team.ID,
		Name:       body.Name,
		VolumeType: volumeType,
	})

	switch {
	case dberrors.IsUniqueConstraintViolation(err):
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Volume with name '%s' already exists", body.Name))
		telemetry.ReportError(ctx, "volume already exists", err)
	case err != nil:
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when creating volume")
		telemetry.ReportCriticalError(ctx, "error when creating volume", err)
	default:
		result := api.Volume{
			Id:   volume.ID.String(),
			Name: volume.Name,
		}

		c.JSON(http.StatusCreated, result)
	}
}

func (a *APIStore) getVolumeType(ctx context.Context) string {
	volumeType := a.featureFlags.StringFlag(ctx, feature_flags.DefaultPersistentVolumeType)
	if volumeType == "" {
		volumeType = a.config.DefaultPersistentVolumeType
	}

	return volumeType
}
