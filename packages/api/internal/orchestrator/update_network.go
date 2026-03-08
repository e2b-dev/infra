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
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) UpdateSandboxNetworkConfig(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxID string,
	allowedCIDRs []string,
	deniedCIDRs []string,
) *api.APIError {
	// Normalize bare IPs to CIDR notation (e.g. "8.8.8.8" → "8.8.8.8/32"),
	// matching the creation path in buildNetworkConfig.
	allowedCIDRs = sandbox_network.AddressStringsToCIDRs(allowedCIDRs)
	deniedCIDRs = sandbox_network.AddressStringsToCIDRs(deniedCIDRs)
	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, &sandbox.NotRunningError{SandboxID: sandboxID, State: sbx.State}
		}

		if sbx.Network == nil {
			sbx.Network = &types.SandboxNetworkConfig{}
		}

		sbx.Network.Egress = &types.SandboxNetworkEgressConfig{
			AllowedAddresses: allowedCIDRs,
			DeniedAddresses:  deniedCIDRs,
		}

		return sbx, nil
	}

	var sbxNotFoundErr *sandbox.NotFoundError
	var sbxNotRunningErr *sandbox.NotRunningError

	sbx, err := o.sandboxStore.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		switch {
		case errors.As(err, &sbxNotRunningErr):
			return &api.APIError{Code: http.StatusConflict, ClientMsg: utils.SandboxChangingStateMsg(sandboxID, sbxNotRunningErr.State), Err: err}
		case errors.As(err, &sbxNotFoundErr):
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sandboxID), Err: err}
		default:
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error updating sandbox network config", Err: err}
		}
	}

	if apiErr := o.updateSandboxNetworkOnNode(ctx, sbx, allowedCIDRs, deniedCIDRs); apiErr != nil {
		return apiErr
	}

	return nil
}

func (o *Orchestrator) updateSandboxNetworkOnNode(
	ctx context.Context,
	sbx sandbox.Sandbox,
	allowedCIDRs []string,
	deniedCIDRs []string,
) *api.APIError {
	ctx, span := tracer.Start(ctx, "update-sandbox-network-on-node",
		trace.WithAttributes(
			attribute.String("instance.id", sbx.SandboxID),
		),
	)
	defer span.End()

	node := o.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("Node hosting sandbox '%s' not found", sbx.SandboxID),
			Err:       fmt.Errorf("node '%s' not found for cluster '%s'", sbx.NodeID, sbx.ClusterID),
		}
	}

	client, ctx := node.GetClient(ctx)
	_, err := client.Sandbox.UpdateNetwork(ctx, &orchestratorgrpc.SandboxUpdateNetworkRequest{
		SandboxId:    sbx.SandboxID,
		AllowedCidrs: allowedCIDRs,
		DeniedCidrs:  deniedCIDRs,
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
