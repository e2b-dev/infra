package volumes

import (
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

func (v *VolumeService) processError(err error) error {
	if err == nil {
		return nil
	}

	if os.IsNotExist(err) {
		return status.Error(codes.NotFound, err.Error())
	}

	return status.Error(codes.Internal, err.Error())
}

func tryParseUUID(id string) bool {
	_, err := uuid.Parse(id)

	return err == nil
}
