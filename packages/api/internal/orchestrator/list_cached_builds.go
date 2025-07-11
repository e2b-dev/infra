package orchestrator

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/golang/protobuf/ptypes/empty"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (o *Orchestrator) listCachedBuilds(ctx context.Context, nodeID string) ([]*orchestrator.CachedBuildInfo, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "list-cached-builds")
	defer childSpan.End()

	client, childCtx, err := o.GetClient(childCtx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get GRPC client: %w", err)
	}

	res, err := client.Sandbox.ListCachedBuilds(childCtx, &empty.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	return res.GetBuilds(), nil
}
