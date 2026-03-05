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

func (s *Service) ListDir(ctx context.Context, request *orchestrator.VolumeDirListRequest) (r *orchestrator.VolumeDirListResponse, err error) {
	ctx, span := tracer.Start(ctx, "list directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	if request.GetDepth() != 0 {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusNotImplemented, orchestrator.UserErrorCode_NOT_SUPPORTED, "depth must be zero")
	}

	paths, err := s.buildPaths(request)
	if err != nil {
		return nil, err
	}

	span.AddEvent("listing directory", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
	))

	items, err := os.ReadDir(paths.HostFullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusNotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to read: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to read directory %q: %w", paths.HostFullPath, err)
	}

	var results []*orchestrator.VolumeDirectoryItem
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to get info for item %s/%q: %w", paths.HostFullPath, item.Name(), err)
		}

		entry := toEntryFromOSInfoAndPaths(paths, info)

		results = append(results, &orchestrator.VolumeDirectoryItem{
			Entry: entry,
		})
	}

	return &orchestrator.VolumeDirListResponse{Files: results}, nil
}
