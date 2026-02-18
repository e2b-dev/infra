package volumes

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) Create(_ context.Context, request *orchestrator.VolumeCreateRequest) (r *orchestrator.VolumeCreateResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	volumePath, err := s.buildVolumePath(request.GetVolume(), "")
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	if err := os.MkdirAll(volumePath, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}
