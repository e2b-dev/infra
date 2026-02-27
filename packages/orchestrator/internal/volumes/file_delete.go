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
	relPath := request.GetPath()
	if relPath == "" {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "path cannot be empty")
	}

	fullPath, err := s.buildVolumePath(request.GetVolume(), relPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("deleting file", trace.WithAttributes(
		attribute.String("path", fullPath),
	))

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to delete: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	return &orchestrator.VolumeFileDeleteResponse{}, nil
}
