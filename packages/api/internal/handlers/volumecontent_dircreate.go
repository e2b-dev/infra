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

const defaultDirMode uint32 = 0o777

func (a *APIStore) PostVolumesVolumeIDDir(c *gin.Context, volumeID api.VolumeID, params api.PostVolumesVolumeIDDirParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		mode := defaultDirMode
		if params.Mode != nil {
			mode = *params.Mode
		}

		ownerID := defaultOwnerID
		if params.UserID != nil {
			ownerID = *params.UserID
		}

		groupID := defaultGroupID
		if params.GroupID != nil {
			groupID = *params.GroupID
		}

		parents := false
		if params.CreateParents != nil {
			parents = *params.CreateParents
		}

		_, err := client.Volumes.CreateDir(ctx, &orchestrator.VolumeDirCreateRequest{
			Volume:        toVolumeKey(volume),
			Path:          params.Path,
			Mode:          mode,
			OwnerId:       ownerID,
			GroupId:       groupID,
			CreateParents: parents,
		})
		if err != nil {
			return err
		}

		c.JSON(http.StatusCreated, &api.PostVolumesVolumeIDDirResponse{})

		return nil
	})
}
