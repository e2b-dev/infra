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

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (s *Service) CreateDir(ctx context.Context, request *orchestrator.CreateDirRequest) (r *orchestrator.CreateDirResponse, err error) {
	ctx, span := tracer.Start(ctx, "create directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, errResponse := s.getFilesystemAndPath(ctx, request)
	if errResponse != nil {
		return nil, errResponse.Err()
	}
	defer fs.Close()

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
		if err = ensureDirs(fs, filepath.Dir(path), uid, gid); err != nil {
			return nil, fmt.Errorf("failed to prepare parent directories: %w", err)
		}
	}

	updateDir := true

	if err = fs.Mkdir(path, mode); err != nil {
		if !request.GetCreateParents() || !errors.Is(err, os.ErrExist) {
			return nil, processError(ctx, "failed to create directory", err)
		}

		stat, statErr := fs.Stat(path)
		if statErr != nil {
			return nil, fmt.Errorf("failed to verify existing path %q: %w", path, statErr)
		}
		if !stat.IsDir() {
			return nil, processError(ctx, "failed to create directory", err)
		}

		updateDir = false
	}

	if updateDir {
		if err := fs.Chown(path, int(uid), int(gid)); err != nil {
			return nil, processError(ctx, "failed to chown directory", err)
		}

		// we do this again to avoid the process' umask from automatically 'fixing' our requests.
		if err := fs.Chmod(path, mode); err != nil {
			return nil, processError(ctx, "failed to chmod directory", err)
		}
	}

	stat, err := fs.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	entry := toEntry(path, stat)

	return &orchestrator.CreateDirResponse{Entry: entry}, nil
}

func processError(ctx context.Context, s string, err error) error {
	if errors.Is(err, os.ErrExist) {
		return newAPIError(ctx, codes.AlreadyExists, http.StatusConflict, orchestrator.UserErrorCode_PATH_ALREADY_EXISTS, "%s: %s", s, err.Error()).Err()
	}

	if errors.Is(err, os.ErrNotExist) {
		return newAPIError(ctx, codes.NotFound, http.StatusNotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "%s: %s", s, err.Error()).Err()
	}

	return fmt.Errorf("%s: %w", s, err)
}
