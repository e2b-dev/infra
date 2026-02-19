package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (a *APIStore) GetVolumesVolumeIDStat(c *gin.Context, volumeID api.VolumeID, params api.GetVolumesVolumeIDStatParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		stat, err := client.Volumes.Stat(ctx, &orchestrator.StatRequest{
			Volume: toVolumeKey(volume),
			Path:   params.Path,
		})
		if err != nil {
			return err
		}

		result := toVolumeEntryStat(stat.GetEntry())

		c.JSON(http.StatusOK, &result)

		return nil
	})
}
