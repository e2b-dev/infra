package volumes

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) DeleteVolume(
	ctx context.Context,
	request *orchestrator.DeleteVolumeRequest,
) (r *orchestrator.DeleteVolumeResponse, err error) {
	_, span := tracer.Start(ctx, "delete volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fullPath, err := s.getVolumeRootPath(ctx, request.GetVolume())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("deleting directory", trace.WithAttributes(
		attribute.String("path", fullPath),
	))

	if err := os.RemoveAll(fullPath); err != nil {
		return nil, fmt.Errorf("failed to delete volume: %w", err)
	}

	return &orchestrator.DeleteVolumeResponse{}, nil
}
