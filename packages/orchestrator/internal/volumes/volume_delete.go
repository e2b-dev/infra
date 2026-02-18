package volumes

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *Service) Delete(
	ctx context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (r *orchestrator.VolumeDeleteResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	volumePath, err := s.buildVolumePath(request.GetVolume(), "")
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	logger.L().Info(ctx, "creating directory",
		zap.String("path", volumePath),
	)

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, fmt.Errorf("failed to delete volume: %w", err)
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}
