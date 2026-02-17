package handlers

import (
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

		force := false
		if params.Force != nil {
			force = *params.Force
		}

		if err = fileClient.Send(&orchestrator.VolumeFileCreateRequest{
			Message: &orchestrator.VolumeFileCreateRequest_Start{
				Start: &orchestrator.VolumeFileCreateStart{
					Volume: toVolumeKey(volume),
					Path:   params.Path,
					Mode:   params.Mode,
					Uid:    params.Uid,
					Gid:    params.Gid,
					Force:  force,
				},
			},
		}); err != nil {
			return fmt.Errorf("failed to send start message: %w", err)
		}

		buffer := make([]byte, incomingBufferSize)

		for {
			count, readErr := c.Request.Body.Read(buffer[:cap(buffer)])
			if ignoreEOF(readErr) != nil {
				return fmt.Errorf("failed to read from request body: %w", readErr)
			}

			if count > 0 {
				sendErr := fileClient.Send(&orchestrator.VolumeFileCreateRequest{
					Message: &orchestrator.VolumeFileCreateRequest_Content{
						Content: &orchestrator.VolumeFileCreateContent{Content: buffer[:count]},
					},
				})
				if sendErr != nil {
					return fmt.Errorf("failed to send content message: %w", sendErr)
				}
			}

			if errors.Is(readErr, io.EOF) {
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
		c.JSON(http.StatusCreated, entry)

		return nil
	})
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}
