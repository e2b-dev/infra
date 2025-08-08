package orchestrator

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) UpdateSandboxMetadata(
	ctx context.Context,
	sandboxID string,
	metadata map[string]string,
) *api.APIError {
	childCtx, childSpan := o.tracer.Start(ctx, "update-sandbox-metadata",
		trace.WithAttributes(
			attribute.String("instance.id", sandboxID),
		),
	)
	defer childSpan.End()

	// Get the sandbox info to find the node it's running on
	instanceInfo, err := o.instanceCache.Get(sandboxID)
	if err != nil {
		return &api.APIError{
			Err:       err,
			ClientMsg: "Sandbox not found",
			Code:      404,
		}
	}

	client, childCtx, err := o.GetClient(childCtx, instanceInfo.Node.ID)
	if err != nil {
		return &api.APIError{
			Err:       err,
			ClientMsg: "Failed to connect to sandbox node",
			Code:      500,
		}
	}

	newMetadata := make(map[string]string, len(instanceInfo.Metadata))
	for k, v := range instanceInfo.Metadata {
		newMetadata[k] = v
	}

	for k, v := range metadata {
		newMetadata[k] = v
	}

	// Send metadata to the orchestrator first - don't update local cache until success
	_, err = client.Sandbox.Update(
		childCtx, &orchestrator.SandboxUpdateRequest{
			SandboxId: sandboxID,
			Metadata:  newMetadata,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return &api.APIError{
			Err:       err,
			ClientMsg: "Failed to update sandbox metadata",
			Code:      500,
		}
	}

	// Only update local cache after orchestrator call succeeds
	instanceInfo.Metadata = newMetadata

	telemetry.ReportEvent(childCtx, "Updated sandbox metadata")

	return nil
}
