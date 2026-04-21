package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) UpdateSandboxMetadata(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxID string,
	metadata map[string]string,
) *api.APIError {
	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, &sandbox.NotRunningError{SandboxID: sandboxID, State: sbx.State}
		}

		sbx.Metadata = metadata

		return sbx, nil
	}

	var sbxNotRunningErr *sandbox.NotRunningError

	sbx, err := o.sandboxStore.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		switch {
		case errors.As(err, &sbxNotRunningErr):
			return &api.APIError{Code: http.StatusConflict, ClientMsg: utils.SandboxChangingStateMsg(sandboxID, sbxNotRunningErr.State), Err: err}
		case errors.Is(err, sandbox.ErrNotFound):
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sandboxID), Err: err}
		default:
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error updating sandbox metadata", Err: err}
		}
	}

	return o.updateSandboxMetadataOnNode(ctx, sbx, metadata)
}

func (o *Orchestrator) updateSandboxMetadataOnNode(
	ctx context.Context,
	sbx sandbox.Sandbox,
	metadata map[string]string,
) *api.APIError {
	ctx, span := tracer.Start(ctx, "update-sandbox-metadata-on-node",
		trace.WithAttributes(
			attribute.String("instance.id", sbx.SandboxID),
		),
	)
	defer span.End()

	node := o.getOrConnectNode(ctx, sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("Node hosting sandbox '%s' not found", sbx.SandboxID),
			Err:       fmt.Errorf("node '%s' not found for cluster '%s'", sbx.NodeID, sbx.ClusterID),
		}
	}

	client, ctx := node.GetClient(ctx)
	_, err := client.Sandbox.Update(ctx, &orchestratorgrpc.SandboxUpdateRequest{
		SandboxId: sbx.SandboxID,
		Metadata:  &orchestratorgrpc.SandboxMetadataUpdate{Entries: metadata},
	})
	if err != nil {
		grpcErr, ok := status.FromError(err)
		if ok && grpcErr.Code() == codes.NotFound {
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sbx.SandboxID), Err: err}
		}

		err = utils.UnwrapGRPCError(err)
		telemetry.ReportCriticalError(ctx, "failed to update sandbox metadata on node", err)

		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Error applying metadata to sandbox",
			Err:       fmt.Errorf("failed to update sandbox metadata on node: %w", err),
		}
	}

	telemetry.ReportEvent(ctx, "Updated sandbox metadata on node")

	return nil
}
