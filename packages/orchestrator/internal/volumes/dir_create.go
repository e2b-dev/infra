package volumes

import (
	"context"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) CreateDir(ctx context.Context, request *orchestrator.VolumeCreateDirRequest) (*orchestrator.VolumeCreateDirResponse, error) {
	path, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	if err := os.Mkdir(path, os.FileMode(request.GetPermissions())); err != nil { // todo: better error handling
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &orchestrator.VolumeCreateDirResponse{}, nil
}
