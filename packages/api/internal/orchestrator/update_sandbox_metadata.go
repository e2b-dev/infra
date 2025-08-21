package orchestrator

import (
	"context"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) UpdateSandboxMetadata(
	ctx context.Context,
	sbx *instance.InstanceInfo,
	metadata map[string]string,
) *api.APIError {
	childCtx, childSpan := o.tracer.Start(ctx, "update-sandbox-metadata")
	defer childSpan.End()

	client, childCtx, err := o.GetClient(childCtx, sbx.ClusterID, sbx.NodeID)
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
			SandboxId: sbx.SandboxID,
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

	telemetry.ReportEvent(childCtx, "Updated running sandbox metadata")

	return nil
}
