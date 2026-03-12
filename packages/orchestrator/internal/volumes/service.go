package volumes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
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

	config  cfg.Config
	tracker *chrooted.Tracker
}

var _ orchestrator.VolumeServiceServer = (*Service)(nil)

func New(config cfg.Config, tracker *chrooted.Tracker) *Service {
	return &Service{config: config, tracker: tracker}
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

func (s *Service) getVolumeRootPath(volume *orchestrator.VolumeInfo) (string, error) {
	volumeType := volume.GetVolumeType()
	volTypePath, ok := s.config.PersistentVolumeMounts[volumeType]
	if !ok {
		return "", fmt.Errorf("%w: %q", chrooted.ErrVolumeTypeNotFound, volumeType)
	}

	teamID, ok := internal.TryParseUUID(volume.GetTeamId())
	if !ok {
		return "", status.Newf(codes.InvalidArgument, "invalid team ID %q", volume.GetTeamId()).Err()
	}

	volumeID, ok := internal.TryParseUUID(volume.GetVolumeId())
	if !ok {
		return "", status.Newf(codes.InvalidArgument, "invalid volume ID %q", volume.GetVolumeId()).Err()
	}

	return chrooted.BuildVolumeRootPath(volTypePath, teamID, volumeID), nil
}

func (s *Service) getFilesystem(ctx context.Context, request volumeOnly) (*chrooted.Chrooted, error) {
	volume := request.GetVolume()
	volumeType := volume.GetVolumeType()

	teamID, ok := internal.TryParseUUID(volume.GetTeamId())
	if !ok {
		return nil, status.Newf(codes.InvalidArgument, "invalid team ID %q", volume.GetTeamId()).Err()
	}

	volumeID, ok := internal.TryParseUUID(volume.GetVolumeId())
	if !ok {
		return nil, status.Newf(codes.InvalidArgument, "invalid volume ID %q", volume.GetVolumeId()).Err()
	}

	return s.tracker.Get(ctx, volumeType, teamID, volumeID)
}

func (s *Service) getFilesystemAndPath(ctx context.Context, request volumePathRequest) (*chrooted.Chrooted, string, error) {
	fs, err := s.getFilesystem(ctx, request)
	if err != nil {
		return nil, "", fmt.Errorf("failed to build volume path: %w", err)
	}

	path := request.GetPath()
	if !filepath.IsAbs(path) {
		path = "/" + path
	}
	path = filepath.Clean(path)

	return fs, path, nil
}

func (s *Service) isRoot(path string) bool {
	return path == "/"
}

func toEntry(fullVolumePath, symlinkDest string, fileInfo os.FileInfo) *orchestrator.EntryInfo {
	entry := &orchestrator.EntryInfo{
		Name: fileInfo.Name(),
		Path: fullVolumePath,
		Size: fileInfo.Size(),
		Mode: uint32(fileInfo.Mode() & os.ModePerm),
		Type: toType(fileInfo.Mode()),
	}

	if symlinkDest != "" && symlinkDest != fullVolumePath {
		entry.Name = filepath.Base(fullVolumePath)
		entry.SymlinkTarget = &symlinkDest
	}

	if base := getBase(fileInfo.Sys()); base != nil {
		entry.AccessedTime = toTimestampFromSpec(base.Atim)
		entry.CreatedTime = toTimestampFromSpec(base.Ctim)
		entry.ModifiedTime = toTimestampFromSpec(base.Mtim)
		entry.Uid = base.Uid
		entry.Gid = base.Gid
	} else if !fileInfo.ModTime().IsZero() {
		entry.ModifiedTime = toTimestampFromTime(fileInfo.ModTime())
	}

	return entry
}

func getBase(sys any) *syscall.Stat_t {
	st, _ := sys.(*syscall.Stat_t)

	return st
}

func toType(fileType os.FileMode) orchestrator.FileType {
	switch {
	case fileType.IsDir():
		return orchestrator.FileType_FILE_TYPE_DIRECTORY
	case fileType.IsRegular():
		return orchestrator.FileType_FILE_TYPE_FILE
	case fileType&os.ModeSymlink == os.ModeSymlink:
		return orchestrator.FileType_FILE_TYPE_SYMLINK
	default:
		return orchestrator.FileType_FILE_TYPE_UNSPECIFIED
	}
}

func toTimestampFromTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}

	return timestamppb.New(t)
}

func toTimestampFromSpec(spec syscall.Timespec) *timestamppb.Timestamp {
	if spec.Sec == 0 && spec.Nsec == 0 {
		return nil
	}

	return timestamppb.New(time.Unix(spec.Sec, spec.Nsec))
}

func setSpanStatus(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())

		return
	}

	span.SetStatus(otelcodes.Ok, "")
}
