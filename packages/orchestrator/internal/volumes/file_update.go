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

func (s *Service) UpdateFileMetadata(ctx context.Context, request *orchestrator.VolumeFileUpdateRequest) (r *orchestrator.VolumeFileUpdateResponse, err error) {
	ctx, span := tracer.Start(ctx, "update file metadata in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, err := s.getFilesystemAndPath(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	// record provided fields; keep pointers semantics by checking nil
	attrs := []attribute.KeyValue{
		attribute.String("path", path),
	}
	if request.Uid != nil {
		attrs = append(attrs, attribute.Int64("uid", int64(request.GetUid())))
	}
	if request.Gid != nil {
		attrs = append(attrs, attribute.Int64("gid", int64(request.GetGid())))
	}
	if request.Mode != nil {
		attrs = append(attrs, attribute.Int64("mode", int64(request.GetMode())))
	}
	span.AddEvent("updating file metadata", trace.WithAttributes(attrs...))

	if request.Mode != nil {
		if err = fs.Chmod(path, os.FileMode(request.GetMode())); err != nil {
			if os.IsNotExist(err) {
				return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chmod: %q not found.", request.GetPath())
			}

			return nil, fmt.Errorf("failed to update file mode: %w", err)
		}
	}

	if request.Gid != nil || request.Uid != nil {
		uid := -1
		if request.Uid != nil {
			uid = int(request.GetUid())
		}

		gid := -1
		if request.Gid != nil {
			gid = int(request.GetGid())
		}

		if err = fs.Chown(path, uid, gid); err != nil {
			if os.IsNotExist(err) {
				return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to chown: %q not found.", request.GetPath())
			}

			return nil, fmt.Errorf("failed to update file ownership: %w", err)
		}
	}

	fi, err := fs.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to stat: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	entry := toEntry(path, fi)

	return &orchestrator.VolumeFileUpdateResponse{Entry: entry}, nil
}
