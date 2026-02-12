package volumes

import (
	"context"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) DeleteFile(_ context.Context, request *orchestrator.VolumeFileDeleteRequest) (r *orchestrator.VolumeFileDeleteResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	basePath, err := v.buildVolumePath(request.GetVolume())
	if err != nil {
		return nil, err
	}

	fullPath := filepath.Join(basePath, request.GetPath())

	if err := os.Remove(fullPath); err != nil {
		return nil, err
	}

	return &orchestrator.VolumeFileDeleteResponse{}, nil
}
