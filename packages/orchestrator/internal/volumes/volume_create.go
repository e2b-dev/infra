package volumes

import (
	"context"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) Create(_ context.Context, request *orchestrator.VolumeCreateRequest) (*orchestrator.VolumeCreateResponse, error) {
	volumePath, statusErr := v.buildVolumePath(request.GetVolume())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	if err := os.MkdirAll(volumePath, 0o700); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create volume: %v", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}
