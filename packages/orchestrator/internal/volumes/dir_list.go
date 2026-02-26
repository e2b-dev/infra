package volumes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) ListDir(ctx context.Context, request *orchestrator.VolumeDirListRequest) (r *orchestrator.VolumeDirListResponse, err error) {
	ctx, span := tracer.Start(ctx, "list directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, err
	}

	span.AddEvent("listing directory", trace.WithAttributes(
		attribute.String("path", fullPath),
	))

	items, err := os.ReadDir(fullPath)
	if err != nil { // todo: better error handling
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to read: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to read directory %q: %w", fullPath, err)
	}

	var results []*orchestrator.VolumeDirectoryItem
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to get info for item %q: %w", item.Name(), err)
		}

		absPath := filepath.Join(fullPath, item.Name())
		relPath := filepath.Join(request.GetPath(), item.Name())
		entry := toEntryFromOSInfo(absPath, relPath, info)

		results = append(results, &orchestrator.VolumeDirectoryItem{
			Entry: entry,
		})
	}

	return &orchestrator.VolumeDirListResponse{Files: results}, nil
}
