package handlers

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PutSandboxesSandboxIDNetwork(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()

	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	team := auth.MustGetTeamInfo(c)

	body, err := utils.ParseBody[api.PutSandboxesSandboxIDNetworkJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	var allowedEntries []string
	if body.AllowOut != nil {
		allowedEntries = *body.AllowOut
	}

	var deniedEntries []string
	if body.DenyOut != nil {
		deniedEntries = *body.DenyOut
	}

	// Validate denyOut entries are valid IPs/CIDRs (not domains).
	for _, entry := range deniedEntries {
		if !sandbox_network.IsIPOrCIDR(entry) {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("invalid denied CIDR %s", entry))

			return
		}
	}

	// When specifying domains in allowOut, require deny-all (0.0.0.0/0) in denyOut.
	// Without it, domain filtering is meaningless since traffic is allowed by default.
	if len(allowedEntries) > 0 {
		_, allowedDomains := sandbox_network.ParseAddressesAndDomains(allowedEntries)
		hasBlockAll := slices.Contains(deniedEntries, sandbox_network.AllInternetTrafficCIDR)

		if len(allowedDomains) > 0 && !hasBlockAll {
			a.sendAPIStoreError(c, http.StatusBadRequest, ErrMsgDomainsRequireBlockAll)

			return
		}
	}

	if apiErr := a.orchestrator.UpdateSandboxNetworkConfig(ctx, team.ID, sandboxID, allowedEntries, deniedEntries); apiErr != nil {
		telemetry.ReportError(ctx, "error updating sandbox network config", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusNoContent)
}
