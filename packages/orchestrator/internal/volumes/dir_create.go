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
	_, span := tracer.Start(ctx, "create directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	paths, err := s.buildPaths(request)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	uid := utils.DerefOrDefault(request.Uid, defaultOwnerID)   //nolint:protogetter
	gid := utils.DerefOrDefault(request.Gid, defaultGroupID)   //nolint:protogetter
	mode := utils.DerefOrDefault(request.Mode, defaultDirMode) //nolint:protogetter

	span.AddEvent("creating directory", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
		attribute.Int64("uid", int64(uid)),
		attribute.Int64("gid", int64(gid)),
		attribute.Int64("mode", int64(mode)),
	))

	if request.GetCreateParents() {
		// Create only parent directories with defaultDirMode and fix permissions against umask.
		parent := filepath.Dir(paths.HostFullPath)
		if err := ensureParentDirs(paths.HostVolumePath, parent, os.FileMode(defaultDirMode)); err != nil {
			return nil, fmt.Errorf("failed to prepare parent directories: %w", err)
		}
	}

	if err := os.Mkdir(paths.HostFullPath, os.FileMode(mode)); err != nil {
		if os.IsNotExist(err) {
			if !s.isVolumeRootHealthy(ctx, paths.HostVolumePath, request.GetVolume()) {
				return nil, fmt.Errorf("failed to create directory %q: %w", paths.ClientPath, err)
			}

			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to mkdir: parent of %q not found.", request.GetPath())
		}

		if os.IsExist(err) {
			return nil, newAPIError(ctx, codes.AlreadyExists, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_ALREADY_EXISTS, "failed to mkdir: %q already exists.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.Chown(paths.HostFullPath, int(uid), int(gid)); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chown: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to set directory ownership: %w", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := os.Chmod(paths.HostFullPath, os.FileMode(mode)); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chmod: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to set directory mode: %w", err)
	}

	entry, err := toEntryFromPaths(paths)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to stat: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to stat created directory: %w", err)
	}

	return &orchestrator.VolumeDirCreateResponse{Entry: entry}, nil
}
