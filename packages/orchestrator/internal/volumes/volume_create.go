package volumes

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) Create(ctx context.Context, request *orchestrator.VolumeCreateRequest) (r *orchestrator.VolumeCreateResponse, err error) {
	_, span := tracer.Start(ctx, "create volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	paths, err := s.buildPaths(pathlessRequest(request))
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("creating volume", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
	))

	if err := os.MkdirAll(paths.HostFullPath, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	return &orchestrator.VolumeCreateResponse{}, nil
}
