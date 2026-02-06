package handlers

import (
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

	client, tx, err := a.sqlcDB.WithTx(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when beginning transaction", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error beginning transaction")

		return
	}

	defer tx.Rollback(ctx)

	if err := client.DeleteVolume(ctx, queries.DeleteVolumeParams{
		TeamID:   team.ID,
		VolumeID: volume.ID,
	}); err != nil {
		telemetry.ReportCriticalError(ctx, "error when recording volume deletion", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error recording volume deletion")

		return
	}

	if err := cluster.DeleteVolume(ctx, volume); err != nil {
		telemetry.ReportCriticalError(ctx, fmt.Sprintf("failed to delete data for volume %q", volume.ID.String()), err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("failed to delete data for volume %q", volume.ID.String()))

		return
	}

	if err := tx.Commit(ctx); err != nil {
		telemetry.ReportCriticalError(ctx, "error when committing transaction", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error committing transaction")

		return
	}
	c.Status(http.StatusNoContent)
}
