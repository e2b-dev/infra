package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) UpdateSandbox(
	ctx context.Context,
	sandboxID string,
	endTime time.Time,
	clusterID uuid.UUID,
	nodeID string,
) error {
	childCtx, childSpan := tracer.Start(ctx, "update-sandbox",
		trace.WithAttributes(
			attribute.String("instance.id", sandboxID),
		),
	)
	defer childSpan.End()

	client, childCtx, err := o.GetClient(childCtx, clusterID, nodeID)
	if err != nil {
		return fmt.Errorf("failed to get client '%s': %w", nodeID, err)
	}

	_, err = client.Sandbox.Update(
		childCtx, &orchestrator.SandboxUpdateRequest{
			SandboxId: sandboxID,
			EndTime:   timestamppb.New(endTime),
		},
	)
	if err != nil {
		grpcErr, ok := status.FromError(err)
		if ok && grpcErr.Code() == codes.NotFound {
			return ErrSandboxNotFound
		}

		err = utils.UnwrapGRPCError(err)
		return fmt.Errorf("failed to update sandbox '%s': %w", sandboxID, err)
	}

	telemetry.ReportEvent(childCtx, "Updated sandbox")

	return nil
}
