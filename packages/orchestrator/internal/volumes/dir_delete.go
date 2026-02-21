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

type removeFunc func(path string) error

func (s *Service) DeleteDir(ctx context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, err
	}

	var fn removeFunc
	if request.GetRecursive() {
		fn = os.RemoveAll
	} else {
		fn = os.Remove
	}

	logger.L().Info(ctx, "removing directory",
		zap.String("path", fullPath),
	)

	if err := fn(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, "path_not_found", "failed to delete: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
