package volumes

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type makeDir func(path string, perm os.FileMode) error

func (s *Service) CreateDir(_ context.Context, request *orchestrator.VolumeDirCreateRequest) (r *orchestrator.VolumeDirCreateResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	uid := utils.DerefOrDefault(request.Uid, defaultOwnerID)   //nolint:protogetter
	gid := utils.DerefOrDefault(request.Gid, defaultGroupID)   //nolint:protogetter
	mode := utils.DerefOrDefault(request.Mode, defaultDirMode) //nolint:protogetter

	var fn makeDir
	if request.GetCreateParents() {
		fn = os.MkdirAll
	} else {
		fn = os.Mkdir
	}
	if err := fn(fullPath, os.FileMode(mode)); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.Chown(fullPath, int(uid), int(gid)); err != nil {
		return nil, fmt.Errorf("failed to set directory ownership: %w", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := os.Chmod(fullPath, os.FileMode(mode)); err != nil {
		return nil, fmt.Errorf("failed to set directory mode: %w", err)
	}

	stat, err := os.Lstat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat created directory: %w", err)
	}

	return &orchestrator.VolumeDirCreateResponse{
		Entry: toEntry(stat),
	}, nil
}
