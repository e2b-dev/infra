package volumes

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *Service) Create(ctx context.Context, request *orchestrator.VolumeCreateRequest) (r *orchestrator.VolumeCreateResponse, err error) {
	volumePath, err := s.buildVolumePath(request.GetVolume(), "")
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	logger.L().Info(ctx, "creating volume",
		zap.String("path", volumePath),
	)

	if err := os.MkdirAll(volumePath, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}
