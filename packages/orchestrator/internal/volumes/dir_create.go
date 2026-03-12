package volumes

import (
	"context"
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
		if err := fs.MkdirAll(path, os.FileMode(mode)); err != nil {
			return nil, fmt.Errorf("failed to prepare parent directories: %w", err)
		}
	} else if err := fs.MkdirAll(filepath.Dir(path), os.FileMode(defaultDirMode)); err != nil {
		return nil, fmt.Errorf("failed to prepare parent directories: %w", err)
	}

	if err := fs.MkdirAll(path, os.FileMode(mode)); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to mkdir: parent of %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	if err := fs.Chown(path, int(uid), int(gid)); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chown: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to set directory ownership: %w", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := fs.Chmod(path, os.FileMode(mode)); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chmod: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to set directory mode: %w", err)
	}

	fi, err := fs.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to stat: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to stat created directory: %w", err)
	}

	entry := toEntry(path, fi)

	return &orchestrator.VolumeDirCreateResponse{Entry: entry}, nil
}
