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
	sandboxnetwork "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
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
		AllowedAddresses: sharedutils.DerefOrDefault(body.AllowOut, nil),
		DeniedAddresses:  sharedutils.DerefOrDefault(body.DenyOut, nil),
	}

	ingressUpdate := &types.SandboxNetworkIngressConfig{
		MaskRequestHost:  body.MaskRequestHost,
		AllowedAddresses: sharedutils.DerefOrDefault(body.AllowIn, nil),
		DeniedAddresses:  sharedutils.DerefOrDefault(body.DenyIn, nil),
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
// - denyOut entries must be IPs or CIDRs (not domains, no ports)
// - allowOut entries can be IPs, CIDRs, or domain names (no ports)
// - when allowOut contains domains, denyOut must include 0.0.0.0/0
func validateEgressRules(allowOut, denyOut []string) *api.APIError {
	denyRules, err := sandboxnetwork.ParseRules(denyOut)
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
				Err:       fmt.Errorf("invalid deny out entry %q: domains are not supported in deny rules", rule.Host),
				ClientMsg: fmt.Sprintf("invalid deny out entry %q: domains are not supported in deny rules", rule.Host),
			}
		}

		if rule.HasPort() {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid deny out entry %q: port-specific rules are not supported for egress", rule.Host),
				ClientMsg: fmt.Sprintf("invalid deny out entry %q: port-specific rules are not supported for egress", rule.Host),
			}
		}
	}

	allowRules, err := sandboxnetwork.ParseRules(allowOut)
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
		} else if rule.HasPort() {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid allow out entry %q: port-specific rules are not supported for egress", rule.Host),
				ClientMsg: fmt.Sprintf("invalid allow out entry %q: port-specific rules are not supported for egress", rule.Host),
			}
		}
	}

	if hasDomains {
		hasBlockAll := slices.ContainsFunc(denyRules, func(r sandboxnetwork.Rule) bool {
			return r.Host == sandboxnetwork.AllInternetTrafficCIDR && r.AllPorts()
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
	denyRules, err := sandboxnetwork.ParseRules(denyIn)
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
				Err:       fmt.Errorf("invalid deny in entry %q: domains are not supported for ingress rules", rule.Host),
				ClientMsg: fmt.Sprintf("invalid deny in entry %q: domains are not supported for ingress rules", rule.Host),
			}
		}
	}

	allowRules, err := sandboxnetwork.ParseRules(allowIn)
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
				Err:       fmt.Errorf("invalid allow in entry %q: domains are not supported for ingress rules", rule.Host),
				ClientMsg: fmt.Sprintf("invalid allow in entry %q: domains are not supported for ingress rules", rule.Host),
			}
		}
	}

	if len(allowRules) > 0 {
		hasBlockAll := slices.ContainsFunc(denyRules, func(r sandboxnetwork.Rule) bool {
			return r.Host == sandboxnetwork.AllInternetTrafficCIDR && r.AllPorts()
		})

		if !hasBlockAll {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("allow in requires deny in to include 0.0.0.0/0 (ALL_TRAFFIC)"),
				ClientMsg: ErrMsgAllowInRequiresBlockAll,
			}
		}
	}

	return nil
}
