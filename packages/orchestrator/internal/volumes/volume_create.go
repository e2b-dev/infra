package volumes

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) Create(_ context.Context, request *orchestrator.VolumeCreateRequest) (r *orchestrator.VolumeCreateResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	volumePath, err := v.buildVolumePath(request.GetVolume(), "")
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	if err := os.MkdirAll(volumePath, 0o700); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create volume: %v", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}
