package volumes

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultDirMode  uint32 = 0o777
	defaultFileMode uint32 = 0o666
	defaultOwnerID  uint32 = 1000
	defaultGroupID  uint32 = 1000
)

type Service struct {
	orchestrator.UnimplementedVolumeServiceServer

	config cfg.Config
}

var _ orchestrator.VolumeServiceServer = (*Service)(nil)

func New(config cfg.Config) *Service {
	return &Service{config: config}
}

func BuildVolumePathParts(teamID, volumeID string) []string {
	return []string{
		fmt.Sprintf("team-%s", teamID),
		fmt.Sprintf("vol-%s", volumeID),
	}
}

func (s *Service) buildVolumePath(volume *orchestrator.VolumeInfo, subPath string) (string, error) {
	volumeType := volume.GetVolumeType()
	volTypePath, ok := s.config.PersistentVolumeMounts[volumeType]
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

	volumeParts := append([]string{volTypePath}, BuildVolumePathParts(teamID, volumeID)...)
	volumePath := filepath.Join(volumeParts...)
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

func tryParseUUID(id string) bool {
	_, err := uuid.Parse(id)

	return err == nil
}
