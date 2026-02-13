package handlers

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (a *APIStore) DeleteVolumesVolumeIDDir(c *gin.Context, volumeID api.VolumeID, params api.DeleteVolumesVolumeIDDirParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		_, err := client.Volumes.DeleteDir(ctx, &orchestrator.VolumeDirDeleteRequest{
			Volume:    toVolumeKey(volume),
			Path:      params.Path,
			Recursive: utils.DerefOrDefault(params.Recursive, false),
		})
		if err != nil {
			return err
		}

		return nil
	})
}
