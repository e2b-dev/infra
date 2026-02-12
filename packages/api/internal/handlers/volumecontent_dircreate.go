package handlers

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (a *APIStore) PostVolumesVolumeIDDir(c *gin.Context, volumeID api.VolumeID, params api.PostVolumesVolumeIDDirParams) {
	a.executeOnOrchestrator(c, func(ctx context.Context, client *clusters.GRPCClient) error {
		_, err := client.Volumes.CreateDir(ctx, &orchestrator.VolumeCreateDirRequest{
			Path:     params.Path,
			VolumeId: volumeID,
		})

		return err
	})
}
