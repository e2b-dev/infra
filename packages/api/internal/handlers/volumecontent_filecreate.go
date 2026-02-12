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
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const incomingBufferSize = 1024 * 1024 // 1MB

func (a *APIStore) PostVolumesVolumeIDFile(c *gin.Context, volumeID api.VolumeID, params api.PostVolumesVolumeIDFileParams) {
	defer c.Request.Body.Close()

	a.executeOnOrchestrator(c, func(ctx context.Context, client *clusters.GRPCClient) error {
		fileClient, err := client.Volumes.CreateFile(ctx)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		if err = fileClient.Send(&orchestrator.VolumeFileCreateRequest{
			Message: &orchestrator.VolumeFileCreateRequest_Start{
				Start: &orchestrator.VolumeFileCreateStart{
					VolumeId: volumeID,
					Path:     params.Path,
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

		c.JSON(http.StatusOK, &api.PostVolumesVolumeIDFileResponse{})

		return nil
	})
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}
