package volumes

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *Service) DeleteDir(ctx context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	ctx, span := tracer.Start(ctx, "delete directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, err := s.getFilesystemAndPath(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	if s.isRoot(path) {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "cannot delete root directory")
	}

	span.AddEvent("removing directory", trace.WithAttributes(
		attribute.String("path", path),
	))

	if _, _, err := fs.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to delete: %q not found.", request.GetPath())
		}

		logger.L().Warn(ctx, "failed to stat path before deleting", zap.Error(err))
	}

	// we can skip the "is not exist" errors, b/c that's what we're trying to do anyway
	if err := fs.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
