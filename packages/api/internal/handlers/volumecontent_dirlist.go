package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (a *APIStore) GetVolumesVolumeIDDir(c *gin.Context, volumeID api.VolumeID, params api.GetVolumesVolumeIDDirParams) {
	a.executeOnOrchestrator(c, func(ctx context.Context, client *clusters.GRPCClient) error {
		response, err := client.Volumes.ListDir(ctx, &orchestrator.VolumeListDirRequest{
			VolumeId: volumeID,
			Path:     params.Path,
		})
		if err != nil {
			return err
		}

		files := make([]api.VolumeStat, 0, len(response.GetFiles()))
		for _, item := range response.GetFiles() {
			entry := item.GetEntry()
			if entry == nil {
				continue
			}

			files = append(files, toVolumeStat(entry))
		}

		c.JSON(http.StatusOK, &api.GetVolumesVolumeIDDirResponse{
			JSON200: &api.VolumeDirectoryListing{
				Files: files,
			},
		})

		return nil
	})
}
