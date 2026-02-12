package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var (
	ErrExpectedStartMessage           = errors.New("expected start message")
	ErrExpectedContentOrFinishMessage = errors.New("expected content or finish message")
)

func (a *APIStore) GetVolumesVolumeIDFile(c *gin.Context, volumeID api.VolumeID, params api.GetVolumesVolumeIDFileParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		fileClient, err := client.Volumes.GetFile(ctx, &orchestrator.VolumeFileGetRequest{
			Volume: toVolumeKey(volume),
			Path:   params.Path,
		})
		if err != nil {
			return fmt.Errorf("failed to get file: %w", err)
		}

		start, err := fileClient.Recv()
		if err != nil {
			return fmt.Errorf("failed to receive start message: %w", err)
		}

		startMsg := start.GetStart()
		if startMsg == nil {
			return ErrExpectedStartMessage
		}

		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Header().Set("Content-Type", "application/octet-stream")
		c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", params.Path))
		c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", startMsg.GetSize()))

		for {
			message, err := fileClient.Recv()
			if err != nil {
				return fmt.Errorf("failed to receive message: %w", err)
			}

			if f := message.GetFinish(); f != nil {
				break
			}

			content := message.GetContent()
			if content == nil {
				return ErrExpectedContentOrFinishMessage
			}

			if _, err = c.Writer.Write(content.GetContent()); err != nil {
				return fmt.Errorf("failed to write message: %w", err)
			}
		}

		return nil
	})
}
