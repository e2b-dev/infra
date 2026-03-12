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
			return nil, processError(ctx, "failed to create directory (with parents)", err)
		}
	} else if err := fs.Mkdir(path, os.FileMode(mode)); err != nil {
		return nil, processError(ctx, "failed to create directory", err)
	}

	if err := fs.Chown(path, int(uid), int(gid)); err != nil {
		return nil, processError(ctx, "failed to chown directory", err)
	}

	// we do this again to avoid the process' umask from automatically 'fixing' our requests.
	if err := fs.Chmod(path, os.FileMode(mode)); err != nil {
		return nil, processError(ctx, "failed to chmod directory", err)
	}

	fi, err := fs.Stat(path)
	if err != nil {
		return nil, processError(ctx, "failed to stat directory", err)
	}

	entry := toEntry(path, fi)

	return &orchestrator.VolumeDirCreateResponse{Entry: entry}, nil
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
