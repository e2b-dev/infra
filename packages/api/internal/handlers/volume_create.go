package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	feature_flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
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

	ctx = feature_flags.AddToContext(ctx, feature_flags.VolumeContext(body.Name))

	volumeType, done := a.getVolumeType(ctx, c)
	if done {
		return
	}

	volume, err := a.sqlcDB.CreateVolume(ctx, queries.CreateVolumeParams{
		TeamID:     team.ID,
		Name:       body.Name,
		VolumeType: volumeType,
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

func (a *APIStore) getVolumeType(ctx context.Context, c *gin.Context) (string, bool) {
	volumeType := a.featureFlags.StringFlag(ctx, feature_flags.DefaultPersistentVolumeType)
	if volumeType == "" {
		volumeType = a.config.DefaultPersistentVolumeType
	}
	if volumeType == "" {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Default persistent volume type is not configured")
		telemetry.ReportCriticalError(ctx, "default persistent volume type is not configured", nil)

		return "", true
	}

	return volumeType, false
}
