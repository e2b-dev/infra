package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) DeleteVolumesVolumeID(c *gin.Context, volumeID api.VolumeID) {
	ctx := c.Request.Context()

	volume, team, ok := a.getVolume(c, volumeID)
	if !ok {
		return
	}

	clusterID := utils.WithClusterFallback(team.ClusterID)
	cluster, ok := a.clusters.GetClusterById(clusterID)
	if !ok {
		telemetry.ReportCriticalError(ctx, fmt.Sprintf("cluster with ID '%s' not found", clusterID), nil)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("cluster with id %s not found", clusterID))

		return
	}

	// from now on we don't want the operation to be canceled halfway through
	ctx = context.WithoutCancel(ctx)

	if err := cluster.DeleteVolume(ctx, volume); err != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting volume", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting volume")

		return
	}

	if err := a.sqlcDB.DeleteVolume(ctx, queries.DeleteVolumeParams{
		TeamID:   team.ID,
		VolumeID: volume.ID,
	}); err != nil {
		telemetry.ReportCriticalError(ctx, "error when recording volume deletion", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error recording volume deletion")

		return
	}

	c.Status(http.StatusNoContent)
}
