package volumes

import (
	"context"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) CreateDir(_ context.Context, request *orchestrator.VolumeCreateDirRequest) (*orchestrator.VolumeCreateDirResponse, error) {
	path, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	if err := os.Mkdir(path, os.FileMode(request.GetPermissions())); err != nil { // todo: better error handling
		return nil, v.processError(err)
	}

	stat, err := os.Stat(path)
	if err != nil {
		return nil, v.processError(err)
	}

	return &orchestrator.VolumeCreateDirResponse{
		Entry: toEntry(stat),
	}, nil
}
