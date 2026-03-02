package volumes

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) Delete(
	ctx context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (r *orchestrator.VolumeDeleteResponse, err error) {
	_, span := tracer.Start(ctx, "delete volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	paths, err := s.buildPaths(pathlessRequest(request))
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("deleting directory", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
	))

	if err := os.RemoveAll(paths.HostFullPath); err != nil {
		return nil, fmt.Errorf("failed to delete volume: %w", err)
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}
