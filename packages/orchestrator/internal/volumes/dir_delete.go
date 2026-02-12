package volumes

import (
	"context"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) DeleteDir(_ context.Context, request *orchestrator.VolumeDirDeleteRequest) (*orchestrator.VolumeDirDeleteResponse, error) {
	path, statusErr := v.buildVolumePath(request.GetVolume())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	if err := os.Remove(path); err != nil { // todo: better error handling
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
