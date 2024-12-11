package server

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *server) ListCachedBuilds(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListCachedBuildsResponse, error) {
	_, childSpan := s.tracer.Start(ctx, "list-cached-templates")
	defer childSpan.End()

	var builds []*orchestrator.CachedBuildInfo

	for key, item := range s.templateCache.Items() {
		builds = append(builds, &orchestrator.CachedBuildInfo{
			BuildId:        key,
			ExpirationTime: timestamppb.New(item.ExpiresAt()),
		})
	}

	return &orchestrator.SandboxListCachedBuildsResponse{
		Builds: builds,
	}, nil
}
