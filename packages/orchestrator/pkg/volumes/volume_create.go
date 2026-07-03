//go:build linux

package volumes

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) CreateVolume(ctx context.Context, request *orchestrator.CreateVolumeRequest) (r *orchestrator.CreateVolumeResponse, err error) {
	_, span := tracer.Start(ctx, "create volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	vol := request.GetVolume()
	volumeType := vol.GetVolumeType()

	teamID, ok := pkg.TryParseUUID(vol.GetTeamId())
	if !ok {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_INVALID_REQUEST, "invalid team ID %q", vol.GetTeamId()).Err()
	}

	volumeID, ok := pkg.TryParseUUID(vol.GetVolumeId())
	if !ok {
		return nil, newAPIError(ctx, codes.InvalidArgument, http.StatusBadRequest, orchestrator.UserErrorCode_INVALID_REQUEST, "invalid volume ID %q", vol.GetVolumeId()).Err()
	}

	rootPath, err := s.builder.BuildVolumePath(volumeType, teamID, volumeID)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("creating volume", trace.WithAttributes(
		attribute.String("path", rootPath),
	))

	if err := s.builder.CreateVolume(ctx, volumeType, teamID, volumeID); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	return &orchestrator.CreateVolumeResponse{}, nil
}
