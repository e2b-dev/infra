package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) UpdateSandbox(
	t trace.Tracer,
	ctx context.Context,
	sandboxID string,
	endTime time.Time,
) error {
	childCtx, childSpan := t.Start(ctx, "update-sandbox",
		trace.WithAttributes(
			attribute.String("instance.id", sandboxID),
		),
	)
	defer childSpan.End()

	_, err := o.grpc.Sandbox.Update(ctx, &orchestrator.SandboxUpdateRequest{
		SandboxID: sandboxID,
		EndTime:   timestamppb.New(endTime),
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to update sandbox '%s': %w", sandboxID, err)
	}

	telemetry.ReportEvent(childCtx, "Updated sandbox")

	return nil
}
