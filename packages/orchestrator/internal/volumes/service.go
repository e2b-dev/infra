package volumes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const (
	defaultDirMode  uint32 = 0o777
	defaultFileMode uint32 = 0o666
	defaultOwnerID  uint32 = 1000
	defaultGroupID  uint32 = 1000
)

type VolumeService struct {
	orchestrator.UnimplementedVolumeServiceServer

	config cfg.Config
}

var _ orchestrator.VolumeServiceServer = (*VolumeService)(nil)

func New(config cfg.Config) *VolumeService {
	return &VolumeService{config: config}
}

func (v *VolumeService) buildVolumePath(volume *orchestrator.VolumeInfo, subPath string) (string, error) {
	volumeType := volume.GetVolumeType()
	volTypePath, ok := v.config.PersistentVolumeMounts[volumeType]
	if !ok {
		return "", status.Newf(codes.NotFound, "volume type %q not found", volumeType).Err()
	}

	teamID := volume.GetTeamId()
	if !tryParseUUID(teamID) {
		return "", status.Newf(codes.InvalidArgument, "invalid team ID %q", teamID).Err()
	}

	volumeID := volume.GetVolumeId()
	if !tryParseUUID(volumeID) {
		return "", status.Newf(codes.InvalidArgument, "invalid volume ID %q", volumeID).Err()
	}

	volumePath := filepath.Join(volTypePath, fmt.Sprintf("team-%s", teamID), fmt.Sprintf("vol-%s", volumeID))
	if subPath != "" {
		subPath = strings.TrimPrefix(subPath, "/")
		subPath = filepath.Clean(subPath)
		fullPath := filepath.Join(volumePath, subPath)

		result, err := filepath.Rel(volumePath, fullPath)
		if err != nil || strings.HasPrefix(result, "..") {
			return "", status.Newf(codes.InvalidArgument, "invalid relative path %q (%q)", subPath, result).Err()
		}

		volumePath = fullPath
	}

	return volumePath, nil
}

func (v *VolumeService) processError(err error) error {
	if err == nil {
		return nil
	}

	// already a grpc error?
	if _, ok := status.FromError(err); ok {
		return err
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
