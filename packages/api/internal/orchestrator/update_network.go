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
	allowedEntries []string,
	deniedEntries []string,
) *api.APIError {
	// Split allowed entries into CIDRs/IPs and domains, matching the creation
	// path in buildNetworkConfig.
	allowedAddresses, allowedDomains := sandbox_network.ParseAddressesAndDomains(allowedEntries)

	// If allowed domains are provided, add the default nameserver so the sandbox
	// can resolve domain names — same as the creation path.
	if len(allowedDomains) > 0 {
		allowedAddresses = append(allowedAddresses, sandbox_network.DefaultNameserver)
	}

	egress := &orchestratorgrpc.SandboxNetworkEgressConfig{
		AllowedCidrs:   sandbox_network.AddressStringsToCIDRs(allowedAddresses),
		DeniedCidrs:    sandbox_network.AddressStringsToCIDRs(deniedEntries),
		AllowedDomains: allowedDomains,
	}

	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, &sandbox.NotRunningError{SandboxID: sandboxID, State: sbx.State}
		}

		if sbx.Network == nil {
			sbx.Network = &types.SandboxNetworkConfig{}
		}

		sbx.Network.Egress = &types.SandboxNetworkEgressConfig{
			AllowedAddresses: allowedEntries,
			DeniedAddresses:  deniedEntries,
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
