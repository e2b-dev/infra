package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var ErrExpectedStartMessage = errors.New("expected start message")

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

		c.Writer.Header().Set("Content-Type", "application/octet-stream")
		c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(params.Path)))
		c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", startMsg.GetSize()))
		c.Writer.WriteHeader(http.StatusOK)

		// we cannot send an error below here, as we've already returned a 200.
		// the best we can do on error is log and abort the connection, which should
		// tell the client that the download failed.

		streamFileContent(ctx, fileClient, c)

		return nil
	})
}

func streamFileContent(ctx context.Context, fileClient grpc.ServerStreamingClient[orchestrator.VolumeFileGetResponse], c *gin.Context) {
	for {
		message, err := fileClient.Recv()
		if err != nil {
			logger.L().Error(ctx, "failed to receive message from file client", zap.Error(err))

			return
		}

		if f := message.GetFinish(); f != nil {
			break
		}

		content := message.GetContent()
		if content == nil {
			logger.L().Error(ctx, "received a nil content message")

			return
		}

		if _, err = c.Writer.Write(content.GetContent()); err != nil {
			logger.L().Error(ctx, "failed to write content to response writer", zap.Error(err))

			return
		}
	}
}
