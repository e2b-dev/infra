package volumes

import (
	"context"
	"fmt"
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

var _ orchestrator.VolumeServiceServer = (*VolumeService)(nil)

func New(config cfg.Config) *VolumeService {
	return &VolumeService{config: config}
}

// validVolumeNameRegex matches volume names that can be used as directory names on Linux.
// This is also part of the strategy to prevent path traversal attacks.
var validVolumeNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func isValidVolumeName(name string) bool {
	return validVolumeNameRegex.MatchString(name)
}

func (v *VolumeService) buildVolumePath(volumeType, teamId, volumeName string) (string, error) {
	if !isValidVolumeName(volumeName) {
		return "", status.Errorf(codes.InvalidArgument, "invalid volume name: %s", volumeName)
	}

	if teamId == "" {
		return "", status.Errorf(codes.InvalidArgument, "team ID is required")
	}

	if volumeType == "" {
		return "", status.Errorf(codes.InvalidArgument, "volume type is required")
	}

	volPath, ok := v.config.PersistentVolumeMounts[volumeType]
	if !ok {
		return "", status.Errorf(codes.NotFound, "volume type %s not found", volumeType)
	}

	volumePath := filepath.Join(volPath, teamId, volumeName)
	if !strings.HasPrefix(volumePath, volPath) {
		return "", status.Errorf(codes.PermissionDenied, "volume path %s is not under %s", volumePath, volPath)
	}

	return volumePath, nil
}

func (v *VolumeService) Create(_ context.Context, request *orchestrator.VolumeCreateRequest) (*orchestrator.VolumeCreateResponse, error) {
	volumePath, err := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeName())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to build volume path: %v", err)
	}

	if err = os.MkdirAll(volumePath, 0700); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create volume: %v", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}

func (v *VolumeService) Delete(
	_ context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (*orchestrator.VolumeDeleteResponse, error) {
	volumePath, err := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeName())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %v", err)
	}

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete volume: %v", err)
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}
