package volumes

import (
	"context"
	"fmt"
	"net/http"
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

	paths, err := s.buildPaths(request)
	if err != nil {
		return nil, err
	}

	span.AddEvent("listing directory", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
	))

	maxDepth := int(request.GetDepth())
	if maxDepth == 0 {
		maxDepth = 1
	}

	results, err := s.listRecursive(paths, maxDepth)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusNotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to read: %q not found.", request.GetPath())
		}

		return nil, err
	}

	return &orchestrator.VolumeDirListResponse{Files: results}, nil
}

func (s *Service) listRecursive(paths volumePaths, depth int) ([]*orchestrator.VolumeDirectoryItem, error) {
	if depth <= 0 {
		return nil, nil
	}

	items, err := os.ReadDir(paths.HostFullPath)
	if err != nil {
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

		if item.IsDir() && depth > 1 {
			childPaths := paths
			childPaths.ClientPath = entry.GetPath()
			childPaths.HostFullPath = filepath.Join(paths.HostFullPath, item.Name())

			children, err := s.listRecursive(childPaths, depth-1)
			if err != nil {
				return nil, err
			}

			results = append(results, children...)
		}
	}

	return results, nil
}
