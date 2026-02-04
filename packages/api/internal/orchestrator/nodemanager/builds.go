package nodemanager

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager")

func (n *Node) SyncBuilds(builds []*orchestrator.CachedBuildInfo) {
	for _, build := range builds {
		n.buildCache.Set(build.GetBuildId(), struct{}{}, time.Until(build.GetExpirationTime().AsTime()))
	}
}

func (n *Node) InsertBuild(buildID string) {
	exists := n.buildCache.Has(buildID)
	if exists {
		return
	}

	// Set the build in the cache for 2 minutes, it should get updated with the correct time from the orchestrator during sync
	n.buildCache.Set(buildID, struct{}{}, 2*time.Minute)
}

func (n *Node) listCachedBuilds(ctx context.Context) ([]*orchestrator.CachedBuildInfo, error) {
	childCtx, childSpan := tracer.Start(ctx, "list-cached-builds")
	defer childSpan.End()

	res, err := n.client.Sandbox.ListCachedBuilds(childCtx, &empty.Empty{})
	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	return res.GetBuilds(), nil
}
