package volumes

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) DeleteFile(ctx context.Context, request *orchestrator.VolumeFileDeleteRequest) (r *orchestrator.VolumeFileDeleteResponse, err error) {
	ctx, span := tracer.Start(ctx, "delete file in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, err := s.getFilesystemAndPath(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	if s.isRoot(path) {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "cannot delete root")
	}

	span.AddEvent("deleting file", trace.WithAttributes(
		attribute.String("path", path),
	))

	if err = fs.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to delete: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	return &orchestrator.VolumeFileDeleteResponse{}, nil
}
