package handlers

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
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

	egressUpdate := &types.SandboxNetworkEgressConfig{
		AllowedAddresses: sutils.DerefOrDefault(body.AllowOut, nil),
		DeniedAddresses:  sutils.DerefOrDefault(body.DenyOut, nil),
	}

	ingressUpdate := &types.SandboxNetworkIngressConfig{
		MaskRequestHost:  body.MaskRequestHost,
		AllowedAddresses: sutils.DerefOrDefault(body.AllowIn, nil),
		DeniedAddresses:  sutils.DerefOrDefault(body.DenyIn, nil),
	}

	if apiErr := validateEgressRules(egressUpdate.AllowedAddresses, egressUpdate.DeniedAddresses); apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	if apiErr := validateIngressRules(ingressUpdate.AllowedAddresses, ingressUpdate.DeniedAddresses); apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	if apiErr := a.orchestrator.UpdateSandboxNetworkConfig(ctx, team.ID, sandboxID, egressUpdate, ingressUpdate); apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error updating sandbox network config", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusNoContent)
}

// validateEgressRules validates egress allow/deny rules:
// - entries must be valid host[:port] strings
// - denyOut hosts must be valid IPs or CIDRs (not domains)
// - allowOut hosts can be IPs, CIDRs, or domain names
// - when allowOut contains domains, denyOut must include 0.0.0.0/0
func validateEgressRules(allowOut, denyOut []string) *api.APIError {
	denyRules, err := sandbox_network.ParseRules(denyOut)
	if err != nil {
		return &api.APIError{
			Code:      http.StatusBadRequest,
			Err:       fmt.Errorf("invalid deny out entry: %w", err),
			ClientMsg: fmt.Sprintf("invalid deny out entry: %s", err),
		}
	}

	for _, rule := range denyRules {
		if rule.IsDomain {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid deny out entry %s: domains are not supported", rule.Host),
				ClientMsg: fmt.Sprintf("invalid deny out entry %s: domains are not supported", rule.Host),
			}
		}
	}

	allowRules, err := sandbox_network.ParseRules(allowOut)
	if err != nil {
		return &api.APIError{
			Code:      http.StatusBadRequest,
			Err:       fmt.Errorf("invalid allow out entry: %w", err),
			ClientMsg: fmt.Sprintf("invalid allow out entry: %s", err),
		}
	}

	hasDomains := false
	for _, rule := range allowRules {
		if rule.IsDomain {
			hasDomains = true
		}
	}

	if hasDomains {
		hasBlockAll := slices.ContainsFunc(denyRules, func(r sandbox_network.Rule) bool {
			return r.Host == sandbox_network.AllInternetTrafficCIDR && r.AllPorts()
		})

		if !hasBlockAll {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("allow out contains domains but deny out is missing 0.0.0.0/0 (ALL_TRAFFIC)"),
				ClientMsg: ErrMsgDomainsRequireBlockAll,
			}
		}
	}

	return nil
}

// validateIngressRules validates ingress allow/deny rules:
// - entries must be valid CIDR[:port] strings (no domains)
// - when allowIn is set, denyIn must include 0.0.0.0/0
func validateIngressRules(allowIn, denyIn []string) *api.APIError {
	denyRules, err := sandbox_network.ParseRules(denyIn)
	if err != nil {
		return &api.APIError{
			Code:      http.StatusBadRequest,
			Err:       fmt.Errorf("invalid deny in entry: %w", err),
			ClientMsg: fmt.Sprintf("invalid deny in entry: %s", err),
		}
	}

	for _, rule := range denyRules {
		if rule.IsDomain {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid deny in entry %s: domains are not supported", rule.Host),
				ClientMsg: fmt.Sprintf("invalid deny in entry %s: domains are not supported for ingress rules", rule.Host),
			}
		}
	}

	allowRules, err := sandbox_network.ParseRules(allowIn)
	if err != nil {
		return &api.APIError{
			Code:      http.StatusBadRequest,
			Err:       fmt.Errorf("invalid allow in entry: %w", err),
			ClientMsg: fmt.Sprintf("invalid allow in entry: %s", err),
		}
	}

	for _, rule := range allowRules {
		if rule.IsDomain {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid allow in entry %s: domains are not supported", rule.Host),
				ClientMsg: fmt.Sprintf("invalid allow in entry %s: domains are not supported for ingress rules", rule.Host),
			}
		}
	}

	return nil
}
