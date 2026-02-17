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

func (a *APIStore) PostVolumesVolumeIDDir(c *gin.Context, volumeID api.VolumeID, params api.PostVolumesVolumeIDDirParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		parents := false
		if params.CreateParents != nil {
			parents = *params.CreateParents
		}

		response, err := client.Volumes.CreateDir(ctx, &orchestrator.VolumeDirCreateRequest{
			Volume:        toVolumeKey(volume),
			Path:          params.Path,
			Mode:          params.Mode,
			Uid:           params.Uid,
			Gid:           params.Gid,
			CreateParents: parents,
		})
		if err != nil {
			return err
		}

		c.JSON(http.StatusCreated, toVolumeEntryStat(response.GetEntry()))

		return nil
	})
}
