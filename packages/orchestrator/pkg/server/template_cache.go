//go:build linux

package server

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *Server) ListCachedBuilds(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListCachedBuildsResponse, error) {
	_, childSpan := tracer.Start(ctx, "list-cached-templates")
	defer childSpan.End()

	var builds []*orchestrator.CachedBuildInfo

	for _, stats := range s.templateCache.CachedBuildStats(ctx) {
		builds = append(builds, &orchestrator.CachedBuildInfo{
			BuildId:             stats.BuildID,
			MemfileCachedBytes:  stats.MemfileCachedBytes,
			MemfileCachedChunks: stats.MemfileCachedChunks,
			RootfsCachedBytes:   stats.RootfsCachedBytes,
			RootfsCachedChunks:  stats.RootfsCachedChunks,
		})
	}

	return &orchestrator.SandboxListCachedBuildsResponse{
		Builds: builds,
	}, nil
}
