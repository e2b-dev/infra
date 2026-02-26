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

type removeFunc func(path string) error

func (s *Service) DeleteDir(ctx context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	ctx, span := tracer.Start(ctx, "delete directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	relPath := request.GetPath()
	if relPath == "" || relPath == "/" || relPath == "." {
		return nil, newAPIError(ctx, codes.InvalidArgument, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "path cannot be empty")
	}

	rootPath, err := s.buildVolumePath(request.GetVolume(), "")
	if err != nil {
		return nil, err
	}

	fullPath, err := s.buildVolumePath(request.GetVolume(), relPath)
	if err != nil {
		return nil, err
	}

	if filepath.Clean(rootPath) == filepath.Clean(fullPath) {
		return nil, newAPIError(ctx, codes.InvalidArgument, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "cannot delete root directory")
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
			return nil, newAPIError(ctx, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to delete: %q not found.", fullPath)
		}

		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
