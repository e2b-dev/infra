package volumes

import (
	"context"
	"os"
	"path/filepath"

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

func (v *VolumeService) buildVolumePath(volumeType, teamID, volumeID string) (string, *status.Status) {
	volPath, ok := v.config.PersistentVolumeMounts[volumeType]
	if !ok {
		return "", status.Newf(codes.NotFound, "volume type %q not found", volumeType)
	}

	if !tryParseUUID(teamID) {
		return "", status.Newf(codes.InvalidArgument, "invalid team ID %q", teamID)
	}

	if !tryParseUUID(volumeID) {
		return "", status.Newf(codes.InvalidArgument, "invalid volume ID %q", volumeID)
	}

	volumePath := filepath.Join(volPath, teamID, volumeID)

	return volumePath, nil
}

func tryParseUUID(id string) bool {
	_, err := uuid.Parse(id)

	return err == nil
}

func (v *VolumeService) Create(_ context.Context, request *orchestrator.VolumeCreateRequest) (*orchestrator.VolumeCreateResponse, error) {
	volumePath, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	if err := os.MkdirAll(volumePath, 0o700); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create volume: %v", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}

func (v *VolumeService) Delete(
	_ context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (*orchestrator.VolumeDeleteResponse, error) {
	volumePath, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete volume: %s", err.Error())
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}
