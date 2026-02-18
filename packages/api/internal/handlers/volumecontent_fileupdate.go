package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PatchVolumesVolumeIDFile(c *gin.Context, volumeID api.VolumeID, params api.PatchVolumesVolumeIDFileParams) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.PatchVolumesVolumeIDFileJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		response, err := client.Volumes.UpdateFileMetadata(ctx, &orchestrator.VolumeFileUpdateRequest{
			Volume: toVolumeKey(volume),
			Path:   params.Path,
			Mode:   body.Mode,
			Uid:    body.Uid,
			Gid:    body.Gid,
		})
		if err != nil {
			return fmt.Errorf("failed to update file metadata: %w", err)
		}

		c.JSON(http.StatusOK, toVolumeEntryStat(response.GetEntry()))

		return nil
	})
}
