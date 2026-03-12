package volumes

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/go-git/go-billy/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) DeleteDir(ctx context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	ctx, span := tracer.Start(ctx, "delete directory in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, err := s.getFilesystemAndPath(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	if s.isRoot(path) {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT, "cannot delete root directory")
	}

	span.AddEvent("removing directory", trace.WithAttributes(
		attribute.String("path", path),
	))

	if err := s.removeAll(fs, path); err != nil {
		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}

func (s *Service) removeAll(fs billy.Filesystem, path string) error {
	if path == "/" {
		return fmt.Errorf("cannot remove root")
	}

	fi, err := fs.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	if !fi.IsDir() {
		return fs.Remove(path)
	}

	entries, err := fs.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		err := s.removeAll(fs, fs.Join(path, entry.Name()))
		if err != nil {
			return err
		}
	}

	return fs.Remove(path)
}
