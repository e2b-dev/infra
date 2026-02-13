package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const incomingBufferSize = 1024 * 1024 // 1MB

func (a *APIStore) PostVolumesVolumeIDFile(c *gin.Context, volumeID api.VolumeID, params api.PostVolumesVolumeIDFileParams) {
	defer c.Request.Body.Close()

	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		fileClient, err := client.Volumes.CreateFile(ctx)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		mode := defaultFileMode
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

		force := false
		if params.Force != nil {
			force = *params.Force
		}

		if err = fileClient.Send(&orchestrator.VolumeFileCreateRequest{
			Message: &orchestrator.VolumeFileCreateRequest_Start{
				Start: &orchestrator.VolumeFileCreateStart{
					Volume:  toVolumeKey(volume),
					Path:    params.Path,
					Mode:    mode,
					OwnerId: ownerID,
					GroupId: groupID,
					Force:   force,
				},
			},
		}); err != nil {
			return fmt.Errorf("failed to send start message: %w", err)
		}

		buffer := make([]byte, incomingBufferSize)

		for {
			reader := bytes.NewBuffer(buffer)
			count, err := reader.ReadFrom(c.Request.Body)
			if ignoreEOF(err) != nil {
				return fmt.Errorf("failed to read from request body: %w", err)
			}

			err = fileClient.Send(&orchestrator.VolumeFileCreateRequest{
				Message: &orchestrator.VolumeFileCreateRequest_Content{
					Content: &orchestrator.VolumeFileCreateContent{Content: buffer[:count]},
				},
			})
			if err != nil {
				return fmt.Errorf("failed to send content message: %w", err)
			}

			if errors.Is(err, io.EOF) {
				break
			}
		}

		if err := fileClient.Send(&orchestrator.VolumeFileCreateRequest{
			Message: &orchestrator.VolumeFileCreateRequest_Finish{
				Finish: &orchestrator.VolumeFileCreateFinish{},
			},
		}); err != nil {
			return fmt.Errorf("failed to send finish message: %w", err)
		}

		finish, err := fileClient.CloseAndRecv()
		if err != nil {
			return fmt.Errorf("failed to receive finish message: %w", err)
		}

		entry := toVolumeEntryStat(finish.GetEntry())
		c.JSON(http.StatusOK, &api.PostVolumesVolumeIDFileResponse{
			JSON201: &entry,
		})

		return nil
	})
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}
