package volumes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const (
	defaultDirMode  os.FileMode = 0o777
	defaultFileMode os.FileMode = 0o666
	defaultOwnerID  uint32      = 9090
	defaultGroupID  uint32      = 9090
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

type volumeOnly interface {
	GetVolume() *orchestrator.VolumeInfo
}

func (s *Service) getVolumeRootPath(ctx context.Context, volume *orchestrator.VolumeInfo) (string, error) {
	volumeType := volume.GetVolumeType()
	volTypePath, ok := s.config.PersistentVolumeMounts[volumeType]
	if !ok {
		return "", newAPIError(ctx, codes.Internal, http.StatusInternalServerError, orchestrator.UserErrorCode_NOT_SUPPORTED, "unknown volume type")
	}

	teamID, ok := internal.TryParseUUID(volume.GetTeamId())
	if !ok {
		return "", newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_INVALID_REQUEST, "invalid team ID %q", volume.GetTeamId())
	}

	volumeID, ok := internal.TryParseUUID(volume.GetVolumeId())
	if !ok {
		return "", newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_INVALID_REQUEST, "invalid volume ID %q", volume.GetVolumeId())
	}

	return chrooted.BuildVolumeRootPath(volTypePath, teamID, volumeID), nil
}

func (s *Service) getFilesystem(ctx context.Context, request volumeOnly) (*chrooted.Chrooted, error) {
	volume := request.GetVolume()
	volumeType := volume.GetVolumeType()

	teamID, ok := internal.TryParseUUID(volume.GetTeamId())
	if !ok {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusInternalServerError, orchestrator.UserErrorCode_INVALID_REQUEST, "invalid team ID %q", volume.GetTeamId())
	}

	volumeID, ok := internal.TryParseUUID(volume.GetVolumeId())
	if !ok {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusInternalServerError, orchestrator.UserErrorCode_INVALID_REQUEST, "invalid volume ID %q", volume.GetVolumeId())
	}

	chroot, err := s.tracker.Get(ctx, volumeType, teamID, volumeID)
	if err != nil {
		if errors.Is(err, chrooted.ErrVolumeTypeNotFound) {
			return nil, newAPIError(ctx, codes.Internal, http.StatusInternalServerError, orchestrator.UserErrorCode_NOT_SUPPORTED, "volume type not found")
		}

		return nil, newAPIError(ctx, codes.Internal, http.StatusInternalServerError, orchestrator.UserErrorCode_UNKNOWN_USER_ERROR_CODE, "failed to get chroot: %v", err)
	}

	return chroot, nil
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

func ensureDirs(fs *chrooted.Chrooted, dirPath string, uid, gid uint32, mode os.FileMode) error {
	if dirPath == "" {
		return nil
	}

	// Determine which parent directories do not exist yet, up to the volume root.
	var needsUpdates []string
	cur := dirPath
	for {
		if _, _, err := fs.Stat(cur); err == nil {
			if cur == dirPath {
				// there's nothing for us to do here
				return nil
			}

			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat parent directory %q: %w", cur, err)
		}

		needsUpdates = append(needsUpdates, cur)

		next := filepath.Clean(filepath.Dir(cur))
		if next == cur { // reached filesystem root just in case
			break
		}
		cur = next
	}

	if err := fs.MkdirAll(dirPath, mode); err != nil {
		return fmt.Errorf("failed to create parent directories: %w", err)
	}

	// Only chmod the directories that were created by this call (precomputed above).
	// Iterate from highest parent to deepest child for determinism.
	for i := len(needsUpdates) - 1; i >= 0; i-- {
		p := needsUpdates[i]
		if err := fs.Chmod(p, mode); err != nil {
			return fmt.Errorf("failed chmod for created parent directory %q: %w", p, err)
		}

		if err := fs.Chown(p, int(uid), int(gid)); err != nil {
			return fmt.Errorf("failed chown for created parent directory %q: %w", p, err)
		}
	}

	return nil
}
