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
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) UpdateSandboxNetworkConfig(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxID string,
	allowedEntries []string,
	deniedEntries []string,
	allowInternetAccess *bool,
) *api.APIError {
	network := &types.SandboxNetworkConfig{
		Egress: &types.SandboxNetworkEgressConfig{
			AllowedAddresses: allowedEntries,
			DeniedAddresses:  deniedEntries,
		},
	}
	orchNetwork := buildNetworkConfig(network, allowInternetAccess, nil)
	egress := orchNetwork.GetEgress()

	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, &sandbox.NotRunningError{SandboxID: sandboxID, State: sbx.State, Transition: sbx.Transition}
		}

		if sbx.Network == nil {
			sbx.Network = &types.SandboxNetworkConfig{}
		}

		sbx.Network.Egress = &types.SandboxNetworkEgressConfig{
			AllowedAddresses: allowedEntries,
			DeniedAddresses:  deniedEntries,
		}

		if allowInternetAccess != nil {
			sbx.AllowInternetAccess = allowInternetAccess
		}

		return sbx, nil
	}

	var sbxNotRunningErr *sandbox.NotRunningError

	sbx, err := o.sandboxStore.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		switch {
		case errors.As(err, &sbxNotRunningErr):
			// If sandbox is being killed, return 410 Gone instead of 409 Conflict
			if sbxNotRunningErr.State == sandbox.StateKilling {
				killInfo := o.WasSandboxKilled(ctx, teamID, sandboxID)
				return &api.APIError{Code: http.StatusGone, ClientMsg: utils.SandboxKilledMsg(sandboxID, killInfo), Err: err}
			}
			return &api.APIError{Code: http.StatusConflict, ClientMsg: utils.SandboxChangingStateMsg(sandboxID, sbxNotRunningErr.Transition), Err: err}
		case errors.Is(err, sandbox.ErrNotFound):
			// Check if the sandbox was killed (return 410 Gone) vs never existed (return 404 Not Found)
			if killInfo := o.WasSandboxKilled(ctx, teamID, sandboxID); killInfo != nil {
				return &api.APIError{Code: http.StatusGone, ClientMsg: utils.SandboxKilledMsg(sandboxID, killInfo), Err: sandbox.ErrSandboxKilled}
			}
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sandboxID), Err: err}
		default:
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error updating sandbox network config", Err: err}
		}
	}

	// Apply the network update on the orchestrator node.
	return o.updateSandboxNetworkOnNode(ctx, sbx, egress)
}

func (o *Orchestrator) updateSandboxNetworkOnNode(
	ctx context.Context,
	sbx sandbox.Sandbox,
	egress *orchestratorgrpc.SandboxNetworkEgressConfig,
) *api.APIError {
	ctx, span := tracer.Start(ctx, "update-sandbox-network-on-node",
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
		Egress:    egress,
	})
	if err != nil {
		grpcErr, ok := status.FromError(err)
		if ok && grpcErr.Code() == codes.NotFound {
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sbx.SandboxID), Err: err}
		}

		err = utils.UnwrapGRPCError(err)
		telemetry.ReportCriticalError(ctx, "failed to update sandbox network on node", err)

		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Error applying network config to sandbox",
			Err:       fmt.Errorf("failed to update sandbox network on node: %w", err),
		}
	}

	telemetry.ReportEvent(ctx, "Updated sandbox network on node")

	return nil
}
