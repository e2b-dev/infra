package orchestrator

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
)

func (o *Orchestrator) UpdateSandboxNetworkConfig(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxID string,
	allowedCIDRs []string,
	deniedCIDRs []string,
) (*sandbox.Sandbox, *api.APIError) {
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
			return nil, &api.APIError{Code: http.StatusConflict, ClientMsg: utils.SandboxChangingStateMsg(sandboxID, sbxNotRunningErr.State), Err: err}
		case errors.As(err, &sbxNotFoundErr):
			return nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sandboxID), Err: err}
		default:
			return nil, &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error updating sandbox network config", Err: err}
		}
	}

	// TODO(stage-2): call orchestrator gRPC to apply firewall changes on the node
	// err = o.updateSandboxNetworkOnNode(ctx, sandboxID, sbx.ClusterID, sbx.NodeID, allowedCIDRs, deniedCIDRs)

	return &sbx, nil
}
