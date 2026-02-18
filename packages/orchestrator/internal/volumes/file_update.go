package volumes

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *Service) UpdateFileMetadata(ctx context.Context, request *orchestrator.VolumeFileUpdateRequest) (r *orchestrator.VolumeFileUpdateResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	logger.L().Info(ctx, "creating directory",
		zap.String("path", fullPath),
		zap.Uint32p("uid", request.Uid),   // nolint:protogetter // the pointer matters!
		zap.Uint32p("gid", request.Gid),   // nolint:protogetter // the pointer matters!
		zap.Uint32p("mode", request.Mode), // nolint:protogetter // the pointer matters!
	)

	if request.Mode != nil {
		if err = os.Chmod(fullPath, os.FileMode(request.GetMode())); err != nil {
			return nil, fmt.Errorf("failed to update file mode: %w", err)
		}
	}

	if request.Gid != nil || request.Uid != nil {
		uid := -1
		if request.Uid != nil {
			uid = int(request.GetUid())
		}

		gid := -1
		if request.Gid != nil {
			gid = int(request.GetGid())
		}

		if err = os.Chown(fullPath, uid, gid); err != nil {
			return nil, fmt.Errorf("failed to update file ownership: %w", err)
		}
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	return &orchestrator.VolumeFileUpdateResponse{
		Entry: toEntry(request.GetPath(), info),
	}, nil
}
