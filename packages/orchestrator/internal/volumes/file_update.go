package volumes

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) UpdateFileMetadata(_ context.Context, req *orchestrator.VolumeFileUpdateRequest) (r *orchestrator.VolumeFileUpdateResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	fullPath, err := s.buildVolumePath(req.GetVolume(), req.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	if req.Mode != nil {
		if err = os.Chmod(fullPath, os.FileMode(req.GetMode())); err != nil {
			return nil, fmt.Errorf("failed to update file mode: %w", err)
		}
	}

	if req.Gid != nil || req.Uid != nil {
		uid := -1
		if req.Uid != nil {
			uid = int(req.GetUid())
		}

		gid := -1
		if req.Gid != nil {
			gid = int(req.GetGid())
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
		Entry: toEntry(info),
	}, nil
}
