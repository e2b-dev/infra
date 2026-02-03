package volumes

import (
	"context"
	"os"
	"path/filepath"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type VolumeService struct {
	orchestrator.UnimplementedVolumeServiceServer

	config cfg.Config
}

func New(config cfg.Config) *VolumeService {
	return &VolumeService{config: config}
}

func (v *VolumeService) Delete(ctx context.Context, request *orchestrator.VolumeDeleteRequest) (*orchestrator.VolumeDeleteResponse, error) {
	volPath, ok := v.config.PersistentVolumeMounts[request.VolumeType]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "volume type %s not found", request.VolumeType)
	}

	basePath := volPath
	volumePath := filepath.Join(basePath, request.VolumeName)

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete volume: %v", err)
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}

var _ orchestrator.VolumeServiceServer = (*VolumeService)(nil)
