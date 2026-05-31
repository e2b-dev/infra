//go:build linux

package server

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Server) ListCachedBuilds(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListCachedBuildsResponse, error) {
	_, childSpan := tracer.Start(ctx, "list-cached-templates")
	defer childSpan.End()

	var builds []*orchestrator.CachedBuildInfo

	for key, item := range s.templateCache.Items() {
		var metadata *orchestrator.SchedulingMetadata
		if provider, ok := item.Value().(interface {
			SchedulingMetadata() *orchestrator.SchedulingMetadata
		}); ok {
			metadata = provider.SchedulingMetadata()
		}
		builds = append(builds, &orchestrator.CachedBuildInfo{
			BuildId:            key,
			ExpirationTime:     timestamppb.New(item.ExpiresAt()),
			SchedulingMetadata: metadata,
		})
	}

	return &orchestrator.SandboxListCachedBuildsResponse{
		Builds: builds,
	}, nil
}
