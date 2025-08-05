package orchestrator

import (
	"context"
	"net/http"

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
	sbx, err := o.instanceCache.Get(sandboxID)
	if err != nil {
		return &api.APIError{
			Err:       err,
			ClientMsg: "Sandbox not found",
			Code:      http.StatusNotFound,
		}
	}

	client, childCtx, err := o.GetClient(childCtx, sbx.Node.ClusterID, sbx.Node.NodeID)
	if err != nil {
		return &api.APIError{
			Err:       err,
			ClientMsg: "Failed to connect to sandbox node",
			Code:      http.StatusInternalServerError,
		}
	}

	// Replace metadata completely instead of merging
	// Send metadata to the orchestrator first - don't update local cache until success
	_, err = client.Sandbox.Update(
		childCtx, &orchestrator.SandboxUpdateRequest{
			SandboxId: sandboxID,
			Metadata:  metadata,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return &api.APIError{
			Err:       err,
			ClientMsg: "Failed to update sandbox metadata",
			Code:      http.StatusInternalServerError,
		}
	}

	// Only update local cache after orchestrator call succeeds
	sbx.Metadata = metadata

	telemetry.ReportEvent(childCtx, "Updated sandbox metadata")

	return nil
}
