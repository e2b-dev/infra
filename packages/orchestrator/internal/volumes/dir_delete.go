package volumes

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type removeFunc func(path string) error

func (s *Service) DeleteDir(ctx context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	ctx, span := tracer.Start(ctx, "delete directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	relPath := request.GetPath()
	if relPath == "" {
		return nil, newAPIError(ctx, codes.InvalidArgument, "empty_path", "path cannot be empty")
	}

	fullPath, err := s.buildVolumePath(request.GetVolume(), relPath)
	if err != nil {
		return nil, err
	}

	var fn removeFunc
	if request.GetRecursive() {
		fn = os.RemoveAll
	} else {
		fn = os.Remove
	}

	span.AddEvent("removing directory", trace.WithAttributes(
		attribute.String("path", fullPath),
	))

	if err := fn(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, "path_not_found", "failed to delete: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
