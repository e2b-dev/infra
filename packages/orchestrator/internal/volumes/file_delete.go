package volumes

import (
	"context"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) DeleteFile(_ context.Context, request *orchestrator.VolumeFileDeleteRequest) (*orchestrator.VolumeFileDeleteResponse, error) {
	basePath, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	fullPath := filepath.Join(basePath, request.GetPath())

	if err := os.Remove(fullPath); err != nil {
		return nil, v.processError(err)
	}

	return &orchestrator.VolumeFileDeleteResponse{}, nil
}
