package volumes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

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

	uid := utils.DerefOrDefault(request.Uid, defaultOwnerID)   //nolint:protogetter
	gid := utils.DerefOrDefault(request.Gid, defaultGroupID)   //nolint:protogetter
	mode := utils.DerefOrDefault(request.Mode, defaultDirMode) //nolint:protogetter

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

func (s *Service) mkdirWithParents(ctx context.Context, fs *chrooted.Chrooted, path string, mode uint32, uid uint32, gid uint32) (done bool, err error) {
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
	parent := filepath.Dir(path)
	if err := ensureParentDirs(fs, parent, os.FileMode(defaultDirMode)); err != nil {
		return false, fmt.Errorf("failed to prepare parent directories: %w", err)
	}

	if err := fs.MkdirAll(path, os.FileMode(mode)); err != nil {
		return false, processError(ctx, "failed to create directory (with parents)", err)
	}

	if err := fs.Chown(path, int(uid), int(gid)); err != nil {
		return false, processError(ctx, "failed to chown directory", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := fs.Chmod(path, os.FileMode(mode)); err != nil {
		return false, processError(ctx, "failed to chmod directory", err)
	}

	return false, nil
}

func (s *Service) mkdir(ctx context.Context, fs *chrooted.Chrooted, path string, mode uint32, uid uint32, gid uint32) error {
	if err := fs.Mkdir(path, os.FileMode(mode)); err != nil {
		return processError(ctx, "failed to create directory", err)
	}

	if err := fs.Chown(path, int(uid), int(gid)); err != nil {
		return processError(ctx, "failed to chown directory", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := fs.Chmod(path, os.FileMode(mode)); err != nil {
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

func ensureParentDirs(fs *chrooted.Chrooted, dirPath string, mode os.FileMode) error {
	if dirPath == "" {
		return nil
	}

	// Determine which parent directories do not exist yet, up to the volume root.
	var toChmod []string
	cur := dirPath
	for cur != dirPath {
		if fi, _, err := fs.Stat(cur); err == nil {
			if fi.IsDir() {
				break // first existing directory reached
			}
			// exists but not a directory – let MkdirAll surface an error later
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat parent directory %q: %w", cur, err)
		}

		toChmod = append(toChmod, cur)

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
	for i := len(toChmod) - 1; i >= 0; i-- {
		p := toChmod[i]
		if err := fs.Chmod(p, mode); err != nil {
			if os.IsNotExist(err) {
				// Race or unexpected removal; treat as an error to be explicit.
				return fmt.Errorf("failed to chmod created parent directory %q: %w", p, err)
			}

			return fmt.Errorf("failed to set mode for created parent directory %q: %w", p, err)
		}
	}

	return nil
}
