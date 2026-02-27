package volumes

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type makeDir func(path string, perm os.FileMode) error

func (s *Service) CreateDir(ctx context.Context, request *orchestrator.VolumeDirCreateRequest) (r *orchestrator.VolumeDirCreateResponse, err error) {
	_, span := tracer.Start(ctx, "create directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	uid := utils.DerefOrDefault(request.Uid, defaultOwnerID)   //nolint:protogetter
	gid := utils.DerefOrDefault(request.Gid, defaultGroupID)   //nolint:protogetter
	mode := utils.DerefOrDefault(request.Mode, defaultDirMode) //nolint:protogetter

	span.AddEvent("creating directory", trace.WithAttributes(
		attribute.String("path", fullPath),
		attribute.Int64("uid", int64(uid)),
		attribute.Int64("gid", int64(gid)),
		attribute.Int64("mode", int64(mode)),
	))

	var fn makeDir
	if request.GetCreateParents() {
		fn = os.MkdirAll
	} else {
		fn = os.Mkdir
	}
	if err := fn(fullPath, os.FileMode(mode)); err != nil {
		if os.IsNotExist(err) {
			if !s.isVolumeRootHealthy(ctx, request.GetVolume()) {
				return nil, fmt.Errorf("failed to create directory %q: %w", fullPath, err)
			}

			return nil, newAPIError(ctx, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to mkdir: parent of %q not found.", fullPath)
		}

		if os.IsExist(err) {
			return nil, newAPIError(ctx, codes.AlreadyExists, orchestrator.UserErrorCode_PATH_ALREADY_EXISTS, "failed to mkdir: %q already exists.", fullPath)
		}

		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.Chown(fullPath, int(uid), int(gid)); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chown: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to set directory ownership: %w", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := os.Chmod(fullPath, os.FileMode(mode)); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chmod: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to set directory mode: %w", err)
	}

	entry, err := toEntryFromPath(fullPath, request.GetPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to stat: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to stat created directory: %w", err)
	}

	return &orchestrator.VolumeDirCreateResponse{Entry: entry}, nil
}
