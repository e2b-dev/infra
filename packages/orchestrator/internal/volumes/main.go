package volumes

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

// validVolumeNameRegex matches volume names that can be used as directory names on Linux.
// This is also part of the strategy to prevent path traversal attacks.
var validVolumeNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func isValidVolumeName(name string) bool {
	return validVolumeNameRegex.MatchString(name)
}

func (v *VolumeService) Delete(
	_ context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (*orchestrator.VolumeDeleteResponse, error) {
	if !isValidVolumeName(request.GetVolumeName()) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume name: %s", request.GetVolumeName())
	}

	volPath, ok := v.config.PersistentVolumeMounts[request.GetVolumeType()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "volume type %s not found", request.GetVolumeType())
	}

	volumePath := filepath.Join(volPath, request.GetVolumeName())
	if !strings.HasPrefix(volumePath, volPath) {
		return nil, status.Errorf(codes.PermissionDenied, "volume path %s is not under %s", volumePath, volPath)
	}

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete volume: %v", err)
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}

var _ orchestrator.VolumeServiceServer = (*VolumeService)(nil)
