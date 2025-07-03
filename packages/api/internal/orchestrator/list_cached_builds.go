package orchestrator

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (o *Orchestrator) listCachedBuilds(ctx context.Context, nodeID string) ([]*orchestrator.CachedBuildInfo, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "list-cached-builds")
	defer childSpan.End()

	client, clientMd, err := o.GetClient(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get GRPC client: %w", err)
	}

	reqCtx := metadata.NewOutgoingContext(childCtx, clientMd)
	res, err := client.Sandbox.ListCachedBuilds(reqCtx, &empty.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	return res.GetBuilds(), nil
}
