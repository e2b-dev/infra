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

func (s *Service) DeletePath(ctx context.Context, request *orchestrator.DeletePathRequest) (r *orchestrator.DeletePathResponse, err error) {
	ctx, span := tracer.Start(ctx, "delete file in volume")
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
		return nil, newAPIError(ctx,
			codes.InvalidArgument,
			http.StatusBadRequest,
			orchestrator.UserErrorCode_CANNOT_DELETE_ROOT,
			"cannot delete root",
		).Err()
	}

	span.AddEvent("deleting file", trace.WithAttributes(
		attribute.String("path", path),
	))

	// Check if path exists before deletion since RemoveAll doesn't error on missing paths
	if _, err = fs.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx,
				codes.NotFound,
				http.StatusNotFound,
				orchestrator.UserErrorCode_PATH_NOT_FOUND,
				"failed to delete: %q not found.", request.GetPath(),
			).Err()
		}

		return nil, fmt.Errorf("failed to stat path: %w", err)
	}

	if err = fs.RemoveAll(path); err != nil {
		return nil, fmt.Errorf("failed to delete path: %w", err)
	}

	return &orchestrator.DeletePathResponse{}, nil
}
