package volumes

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
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

func (v *VolumeService) buildVolumePath(volumeType, teamId, volumeID string) (string, error) {
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

	if !tryParseUUID(teamId) {
		return "", status.Errorf(codes.InvalidArgument, "invalid team ID %s", teamId)
	}

	if !tryParseUUID(volumeID) {
		return "", status.Errorf(codes.InvalidArgument, "invalid volume ID %s", volumeID)
	}

	volumePath := filepath.Join(volPath, teamId, volumeID)
	if !strings.HasPrefix(volumePath, volPath) {
		return "", status.Errorf(codes.PermissionDenied, "volume path %s is not under %s", volumePath, volPath)
	}

	return volumePath, nil
}

func tryParseUUID(id string) bool {
	_, err := uuid.Parse(id)

	return err == nil
}

func (v *VolumeService) Create(_ context.Context, request *orchestrator.VolumeCreateRequest) (*orchestrator.VolumeCreateResponse, error) {
	volumePath, err := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to build volume path: %v", err)
	}

	if err = os.MkdirAll(volumePath, 0o700); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create volume: %v", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}

func (v *VolumeService) Delete(
	_ context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (*orchestrator.VolumeDeleteResponse, error) {
	volumePath, err := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to build volume path: %s", err.Error())
	}

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete volume: %s", err.Error())
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}
