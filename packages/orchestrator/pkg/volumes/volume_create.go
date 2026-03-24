package volumes

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) CreateVolume(ctx context.Context, request *orchestrator.CreateVolumeRequest) (r *orchestrator.CreateVolumeResponse, err error) {
	_, span := tracer.Start(ctx, "create volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fullPath, err := s.getVolumeRootPath(ctx, request.GetVolume())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("creating volume", trace.WithAttributes(
		attribute.String("path", fullPath),
	))

	if err := os.MkdirAll(fullPath, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	return &orchestrator.CreateVolumeResponse{}, nil
}
