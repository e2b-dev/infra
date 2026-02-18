package volumes

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) DeleteFile(_ context.Context, request *orchestrator.VolumeFileDeleteRequest) (r *orchestrator.VolumeFileDeleteResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	if err := os.Remove(fullPath); err != nil {
		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	return &orchestrator.VolumeFileDeleteResponse{}, nil
}
