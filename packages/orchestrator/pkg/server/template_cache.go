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
		info := &orchestrator.CachedBuildInfo{
			BuildId:        key,
			ExpirationTime: timestamppb.New(item.ExpiresAt()),
		}
		if provider, ok := item.Value().(interface {
			SchedulingMetadata(ctx context.Context) *orchestrator.SchedulingMetadata
		}); ok {
			info.SchedulingMetadata = provider.SchedulingMetadata(ctx)
		}
		builds = append(builds, info)
	}

	return &orchestrator.SandboxListCachedBuildsResponse{
		Builds: builds,
	}, nil
}
