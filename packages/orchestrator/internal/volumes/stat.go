package volumes

import (
	"context"
	"fmt"
	"net/http"
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
	paths, err := s.buildPaths(request)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("stat", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
	))

	info, err := filesystem.GetEntryFromPath(paths.HostFullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx, codes.NotFound, http.StatusBadRequest, orchestrator.UserErrorCode_PATH_NOT_FOUND, "failed to stat: %q not found.", request.GetPath())
		}

		return nil, fmt.Errorf("failed to stat path: %w", err)
	}

	entry := toEntry(paths, info)

	return &orchestrator.StatResponse{Entry: entry}, nil
}
