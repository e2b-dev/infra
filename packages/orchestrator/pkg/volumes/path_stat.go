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

func (s *Service) StatPath(ctx context.Context, request *orchestrator.StatPathRequest) (r *orchestrator.StatPathResponse, err error) {
	ctx, span := tracer.Start(ctx, "stat path in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, errResponse := s.getFilesystemAndPath(ctx, request)
	if errResponse != nil {
		return nil, errResponse.Err()
	}
	defer fs.Close()

	span.AddEvent("stat", trace.WithAttributes(
		attribute.String("path", path),
	))

	info, err := fs.GetEntry(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newAPIError(ctx,
				codes.NotFound,
				http.StatusNotFound,
				orchestrator.UserErrorCode_PATH_NOT_FOUND,
				"failed to stat: %q not found.", request.GetPath(),
			).Err()
		}

		return nil, fmt.Errorf("failed to stat path: %w", err)
	}

	entry := fromEntryInfo(path, info)

	return &orchestrator.StatPathResponse{Entry: entry}, nil
}
