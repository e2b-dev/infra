package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (a *APIStore) DeleteVolumesVolumeIDFile(c *gin.Context, volumeID api.VolumeID, params api.DeleteVolumesVolumeIDFileParams) {
	a.executeOnOrchestrator(c, func(ctx context.Context, client *clusters.GRPCClient) error {
		_, err := client.Volumes.DeleteFile(ctx, &orchestrator.VolumeFileDeleteRequest{
			VolumeId: volumeID,
			Path:     params.Path,
		})
		if err != nil {
			return err
		}

		c.JSON(http.StatusOK, &api.DeleteVolumesVolumeIDFileResponse{})

		return nil
	})
}
