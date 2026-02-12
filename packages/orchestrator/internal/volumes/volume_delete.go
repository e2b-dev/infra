package volumes

import (
	"context"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) Delete(
	_ context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (*orchestrator.VolumeDeleteResponse, error) {
	volumePath, statusErr := v.buildVolumePath(request.GetVolume())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete volume: %s", err.Error())
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}
