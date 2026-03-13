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

	fs, path, errResponse := s.getFilesystemAndPath(ctx, request)
	if errResponse != nil {
		return nil, errResponse.Err()
	}
	defer fs.Close()

	if s.isRoot(path) {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "cannot delete root directory").Err()
	}

	span.AddEvent("removing directory", trace.WithAttributes(
		attribute.String("path", path),
	))

	if _, _, err := fs.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to delete: %q not found.", request.GetPath()).Err()
		}

		logger.L().Error(ctx, "failed to stat directory", zap.String("path", path), zap.Error(err))

		return nil, newAPIError(ctx, codes.Internal, http.StatusInternalServerError, orchestrator.UserErrorCode_UNKNOWN_USER_ERROR_CODE, "failed to stat directory").Err()
	}

	// we can skip the "is not exist" errors, b/c that's what we're trying to do anyway
	if err := fs.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
