package volumes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/filesystem"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const (
	defaultDirMode  uint32 = 0o777
	defaultFileMode uint32 = 0o666
	defaultOwnerID  uint32 = 1000
	defaultGroupID  uint32 = 1000
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/volumes")

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

func toEntryFromOSInfo(absPath, volumeRelPath string, fileInfo os.FileInfo) *orchestrator.EntryInfo {
	entry := filesystem.GetEntryInfo(absPath, fileInfo)

	return toEntry(volumeRelPath, entry)
}

func toEntry(volumeRelPath string, fileInfo filesystem.EntryInfo) *orchestrator.EntryInfo {
	if !strings.HasPrefix(volumeRelPath, "/") {
		volumeRelPath = "/" + volumeRelPath
	}

	entry := &orchestrator.EntryInfo{
		Name:          fileInfo.Name,
		Path:          volumeRelPath,
		Size:          fileInfo.Size,
		Mode:          uint32(fileInfo.Mode & os.ModePerm),
		Uid:           fileInfo.UID,
		Gid:           fileInfo.GID,
		ModifiedTime:  toTimestamp(fileInfo.ModifiedTime),
		SymlinkTarget: fileInfo.SymlinkTarget,
		CreatedTime:   toTimestamp(fileInfo.CreatedTime),
		AccessedTime:  toTimestamp(fileInfo.AccessedTime),
		Type:          toType(fileInfo.Type),
	}

	return entry
}

func toType(fileType filesystem.FileType) orchestrator.FileType {
	switch fileType {
	case filesystem.DirectoryFileType:
		return orchestrator.FileType_FILE_TYPE_DIRECTORY
	case filesystem.FileFileType:
		return orchestrator.FileType_FILE_TYPE_FILE
	case filesystem.SymlinkFileType:
		return orchestrator.FileType_FILE_TYPE_SYMLINK
	default:
		return orchestrator.FileType_FILE_TYPE_UNSPECIFIED
	}
}

func toTimestamp(spec time.Time) *timestamppb.Timestamp {
	if spec.IsZero() {
		return nil
	}

	return timestamppb.New(spec)
}

func setSpanStatus(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())

		return
	}

	span.SetStatus(otelcodes.Ok, "")
}
