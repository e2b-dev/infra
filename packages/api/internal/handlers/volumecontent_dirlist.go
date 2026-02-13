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

func (a *APIStore) GetVolumesVolumeIDDir(c *gin.Context, volumeID api.VolumeID, params api.GetVolumesVolumeIDDirParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		response, err := client.Volumes.ListDir(ctx, &orchestrator.VolumeDirListRequest{
			Volume: toVolumeKey(volume),
			Path:   params.Path,
		})
		if err != nil {
			return err
		}

		files := make(api.VolumeDirectoryListing, 0, len(response.GetFiles()))
		for _, item := range response.GetFiles() {
			entry := item.GetEntry()
			if entry == nil {
				continue
			}

			files = append(files, toVolumeEntryStat(entry))
		}

		c.JSON(http.StatusOK, &api.GetVolumesVolumeIDDirResponse{
			JSON200: &files,
		})

		return nil
	})
}
