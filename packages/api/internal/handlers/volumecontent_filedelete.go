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

func (a *APIStore) DeleteVolumesVolumeIDFile(c *gin.Context, volumeID api.VolumeID, params api.DeleteVolumesVolumeIDFileParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		_, err := client.Volumes.DeleteFile(ctx, &orchestrator.VolumeFileDeleteRequest{
			Volume: toVolumeKey(volume),
			Path:   params.Path,
		})
		if err != nil {
			return err
		}

		c.Status(http.StatusNoContent)

		return nil
	})
}
