package volumes

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *Service) DeleteFile(ctx context.Context, request *orchestrator.VolumeFileDeleteRequest) (r *orchestrator.VolumeFileDeleteResponse, err error) {
	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	logger.L().Info(ctx, "deleting file",
		zap.String("path", fullPath),
	)

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, "path_not_found", "failed to delete: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	return &orchestrator.VolumeFileDeleteResponse{}, nil
}
