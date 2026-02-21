package volumes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *Service) ListDir(ctx context.Context, request *orchestrator.VolumeDirListRequest) (r *orchestrator.VolumeDirListResponse, err error) {
	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, err
	}

	logger.L().Info(ctx, "listing directory",
		zap.String("path", fullPath),
	)

	items, err := os.ReadDir(fullPath)
	if err != nil { // todo: better error handling
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, "path_not_found", "failed to read: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to read directory %q: %w", fullPath, err)
	}

	var results []*orchestrator.VolumeDirectoryItem
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to get info for item %q: %w", item.Name(), err)
		}

		results = append(results, &orchestrator.VolumeDirectoryItem{
			Entry: toEntry(filepath.Join(request.GetPath(), item.Name()), info),
		})
	}

	return &orchestrator.VolumeDirListResponse{Files: results}, nil
}
