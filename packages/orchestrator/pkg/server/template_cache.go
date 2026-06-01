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

	buildMap := make(map[string]*orchestrator.CachedBuildInfo)

	// Templates resident in the cache: expose expiration and scheduling metadata.
	for key, item := range s.templateCache.Items() {
		info := &orchestrator.CachedBuildInfo{
			BuildId:        key,
			ExpirationTime: timestamppb.New(item.ExpiresAt()),
		}
		if provider, ok := item.Value().(interface {
			SchedulingMetadata(context.Context) *orchestrator.SchedulingMetadata
		}); ok {
			info.SchedulingMetadata = provider.SchedulingMetadata(ctx)
		}
		buildMap[key] = info
	}

	// Locally cached diffs: merge in per-build byte stats from the diff store.
	for _, stats := range s.templateCache.CachedBuildStats(ctx) {
		info := buildMap[stats.BuildID]
		if info == nil {
			info = &orchestrator.CachedBuildInfo{BuildId: stats.BuildID}
			buildMap[stats.BuildID] = info
		}
		info.MemfileCachedBytes = stats.MemfileCachedBytes
		info.MemfileTotalBytes = stats.MemfileTotalBytes
		info.RootfsCachedBytes = stats.RootfsCachedBytes
		info.RootfsTotalBytes = stats.RootfsTotalBytes
	}

	builds := make([]*orchestrator.CachedBuildInfo, 0, len(buildMap))
	for _, info := range buildMap {
		builds = append(builds, info)
	}

	return &orchestrator.SandboxListCachedBuildsResponse{
		Builds: builds,
	}, nil
}
