package volumes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/filesystem"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	defaultDirMode  uint32 = 0o777
	defaultFileMode uint32 = 0o666
	defaultOwnerID  uint32 = 9090
	defaultGroupID  uint32 = 9090
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

func BuildVolumePathParts(teamID, volumeID uuid.UUID) []string {
	return []string{
		fmt.Sprintf("team-%s", teamID),
		fmt.Sprintf("vol-%s", volumeID),
	}
}

type volumePathRequest interface {
	GetVolume() *orchestrator.VolumeInfo
	GetPath() string
}

type volumeRequest struct {
	request volumeOnly
}

var _ volumePathRequest = (*volumeRequest)(nil)

func (v volumeRequest) GetVolume() *orchestrator.VolumeInfo {
	return v.request.GetVolume()
}

func (v volumeRequest) GetPath() string {
	return ""
}

type volumeOnly interface {
	GetVolume() *orchestrator.VolumeInfo
}

func pathlessRequest(request volumeOnly) volumePathRequest {
	return &volumeRequest{request: request}
}

type volumePaths struct {
	// HostVolumePath is the absolute path to the root of the volume on the host
	HostVolumePath string

	// ClientPath is the relative path to the file within the volume, prefixed with a "/"
	ClientPath string

	// HostFullPath is the absolute path on the host server to the file or directory
	HostFullPath string
}

func (v volumePaths) isRoot() bool {
	return filepath.Clean(v.HostVolumePath) == filepath.Clean(v.HostFullPath)
}

func (s *Service) buildPaths(request volumePathRequest) (volumePaths, error) {
	volume := request.GetVolume()

	// 1. Resolve Volume Type Path
	volTypePath, ok := s.config.PersistentVolumeMounts[volume.GetVolumeType()]
	if !ok {
		st, _ := status.Newf(codes.NotFound, "volume type %q not found", volume.GetVolumeType()).
			WithDetails(&orchestrator.UnknownVolumeTypeError{VolumeType: volume.GetVolumeType()})

		return volumePaths{}, st.Err()
	}

	// 2. Parse UUIDs
	teamID, ok := internal.TryParseUUID(volume.GetTeamId())
	if !ok {
		return volumePaths{}, status.Newf(codes.InvalidArgument, "invalid team ID %q", volume.GetTeamId()).Err()
	}
	volumeID, ok := internal.TryParseUUID(volume.GetVolumeId())
	if !ok {
		return volumePaths{}, status.Newf(codes.InvalidArgument, "invalid volume ID %q", volume.GetVolumeId()).Err()
	}

	// 3. Construct Base Path
	basePath := filepath.Join(append([]string{volTypePath}, BuildVolumePathParts(teamID, volumeID)...)...)

	// 4. Sanitize Subpath & Build Full Path
	subPath := filepath.Clean("/" + request.GetPath()) // Forces absolute-style cleaning
	fullPath := filepath.Join(basePath, subPath)

	// 5. Security Check (Directory Traversal)
	relPath, err := filepath.Rel(basePath, fullPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return volumePaths{}, status.Errorf(codes.InvalidArgument, "invalid path: traversal detected")
	}

	return volumePaths{
		HostVolumePath: basePath,
		ClientPath:     filepath.Join("/", relPath), // Ensures leading slash
		HostFullPath:   fullPath,
	}, nil
}

func (s *Service) isVolumeRootHealthy(ctx context.Context, basePath string, volume *orchestrator.VolumeInfo) bool {
	stat, err := os.Stat(basePath)
	if err != nil {
		logger.L().Warn(ctx, "failed to stat volume root",
			zap.Error(err),
			zap.String("path", basePath),
			zap.String("volume_type", volume.GetVolumeType()),
			zap.String("volume_id", volume.GetVolumeId()),
			zap.String("team_id", volume.GetTeamId()),
		)

		return false
	}

	if !stat.IsDir() {
		logger.L().Warn(ctx, "volume root is not a directory!",
			zap.Error(err),
			zap.String("path", basePath),
			zap.String("volume_type", volume.GetVolumeType()),
			zap.String("volume_id", volume.GetVolumeId()),
			zap.String("team_id", volume.GetTeamId()),
		)

		return false
	}

	return true
}

func toEntryFromOSInfoAndPaths(paths volumePaths, fileInfo os.FileInfo) *orchestrator.EntryInfo {
	paths.ClientPath = filepath.Join(paths.ClientPath, fileInfo.Name())
	paths.HostFullPath = filepath.Join(paths.HostFullPath, fileInfo.Name())

	entry := filesystem.GetEntryInfo(paths.HostFullPath, fileInfo)

	return toEntry(paths, entry)
}

func toEntryFromPaths(paths volumePaths) (*orchestrator.EntryInfo, error) {
	entry, err := filesystem.GetEntryFromPath(paths.HostFullPath)
	if err != nil {
		return nil, err
	}

	return toEntry(paths, entry), nil
}

func toEntry(paths volumePaths, fileInfo filesystem.EntryInfo) *orchestrator.EntryInfo {
	entry := &orchestrator.EntryInfo{
		Name:          fileInfo.Name,
		Path:          paths.ClientPath,
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
