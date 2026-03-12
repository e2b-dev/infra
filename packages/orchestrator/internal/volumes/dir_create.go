package volumes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (s *Service) CreateDir(ctx context.Context, request *orchestrator.VolumeDirCreateRequest) (r *orchestrator.VolumeDirCreateResponse, err error) {
	ctx, span := tracer.Start(ctx, "create directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, err := s.getFilesystemAndPath(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	uid := utils.DerefOrDefault(request.Uid, defaultOwnerID)                        //nolint:protogetter
	gid := utils.DerefOrDefault(request.Gid, defaultGroupID)                        //nolint:protogetter
	mode := os.FileMode(utils.DerefOrDefault(request.Mode, uint32(defaultDirMode))) //nolint:protogetter

	span.AddEvent("creating directory", trace.WithAttributes(
		attribute.String("path", path),
		attribute.Int64("uid", int64(uid)),
		attribute.Int64("gid", int64(gid)),
		attribute.Int64("mode", int64(mode)),
	))

	if request.GetCreateParents() {
		var done bool
		done, err = s.mkdirWithParents(ctx, fs, path, mode, uid, gid)
		if done {
			return &orchestrator.VolumeDirCreateResponse{}, nil
		}
	} else {
		err = s.mkdir(ctx, fs, path, mode, uid, gid)
	}
	if err != nil {
		return nil, err
	}

	stat, symlink, err := fs.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	entry := toEntry(path, symlink, stat)

	return &orchestrator.VolumeDirCreateResponse{Entry: entry}, nil
}

func (s *Service) mkdirWithParents(ctx context.Context, fs *chrooted.Chrooted, path string, mode os.FileMode, uid, gid uint32) (done bool, err error) {
	stat, err := fs.Lstat(path)
	if err == nil {
		if stat.IsDir() {
			// directory already exists, no need to create it
			return true, nil
		}

		return false, processError(ctx, "path exists and is not a directory", os.ErrExist)
	} else if !os.IsNotExist(err) {
		return false, processError(ctx, "failed to stat directory", err)
	}

	// Create only parent directories with defaultDirMode and fix permissions against umask.
	if err := ensureParentDirs(fs, path, uid, gid, defaultDirMode); err != nil {
		return false, fmt.Errorf("failed to prepare parent directories: %w", err)
	}

	if err := fs.Chown(path, int(uid), int(gid)); err != nil {
		return false, processError(ctx, "failed to chown directory", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := fs.Chmod(path, mode); err != nil {
		return false, processError(ctx, "failed to chmod directory", err)
	}

	return false, nil
}

func (s *Service) mkdir(ctx context.Context, fs *chrooted.Chrooted, path string, mode os.FileMode, uid, gid uint32) error {
	if err := fs.Mkdir(path, mode); err != nil {
		return processError(ctx, "failed to create directory", err)
	}

	if err := fs.Chown(path, int(uid), int(gid)); err != nil {
		return processError(ctx, "failed to chown directory", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := fs.Chmod(path, mode); err != nil {
		return processError(ctx, "failed to chmod directory", err)
	}

	return nil
}

func processError(ctx context.Context, s string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, os.ErrExist) {
		return newAPIError(ctx, codes.AlreadyExists, http.StatusConflict, orchestrator.UserErrorCode_PATH_ALREADY_EXISTS, "%s: %s", s, err.Error())
	}

	if errors.Is(err, os.ErrNotExist) {
		return newAPIError(ctx, codes.NotFound, http.StatusNotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "%s: %s", s, err.Error())
	}

	return fmt.Errorf("%s: %w", s, err)
}
