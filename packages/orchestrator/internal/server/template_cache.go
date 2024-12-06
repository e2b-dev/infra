package server

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *server) SandboxListCachedTemplates(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListCachedBuildsResponse, error) {
	_, childSpan := s.tracer.Start(ctx, "list-cached-templates")
	defer childSpan.End()

	var builds []*orchestrator.CachedBuildInfo

	for key, item := range s.templateCache.Items() {
		buildId := strings.Split(key, "-")[1]
		builds = append(builds, &orchestrator.CachedBuildInfo{
			BuildId:        buildId,
			ExpirationTime: timestamppb.New(item.ExpiresAt()),
		})
	}

	return &orchestrator.SandboxListCachedBuildsResponse{
		Builds: builds,
	}, nil
}
