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

func (s *Service) DeleteDir(ctx context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	ctx, span := tracer.Start(ctx, "delete directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	paths, err := s.buildPaths(request)
	if err != nil {
		return nil, err
	}

	if paths.isRoot() {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "cannot delete root directory")
	}

	span.AddEvent("removing directory", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
	))

	if err := os.RemoveAll(paths.HostFullPath); err != nil {
		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
