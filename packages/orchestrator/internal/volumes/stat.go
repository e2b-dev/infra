package volumes

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/filesystem"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) Stat(ctx context.Context, request *orchestrator.StatRequest) (r *orchestrator.StatResponse, err error) {
	ctx, span := tracer.Start(ctx, "stat path in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()
	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("stat", trace.WithAttributes(
		attribute.String("path", fullPath),
	))

	info, err := filesystem.GetEntryFromPath(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, "path_not_found", "failed to stat: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to stat path: %w", err)
	}

	entry := toEntry(request.GetPath(), info)

	return &orchestrator.StatResponse{Entry: entry}, nil
}

func toEntryFromPath(absPath, volumeRelPath string) (*orchestrator.EntryInfo, error) {
	entry, err := filesystem.GetEntryFromPath(absPath)
	if err != nil {
		return nil, err
	}

	return toEntry(volumeRelPath, entry), nil
}
