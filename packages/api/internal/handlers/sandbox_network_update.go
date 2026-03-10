package handlers

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/idna"

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
		MaskRequestHost:    body.MaskRequestHost,
		AllowedPorts:       intsToUint32s(body.AllowPorts),
		DeniedPorts:        intsToUint32s(body.DenyPorts),
		AllowedClientCIDRs: sutils.DerefOrDefault(body.AllowIn, nil),
		DeniedClientCIDRs:  sutils.DerefOrDefault(body.DenyIn, nil),
	}

	if apiErr := validateEgressRules(egressUpdate.AllowedAddresses, egressUpdate.DeniedAddresses); apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	if apiErr := validateIngressRules(ingressUpdate); apiErr != nil {
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

func intsToUint32s(ints *[]int) []uint32 {
	if ints == nil {
		return nil
	}

	result := make([]uint32, len(*ints))
	for i, v := range *ints {
		result[i] = uint32(v)
	}

	return result
}

// validateEgressRules validates egress allow/deny rules:
// - denyOut entries must be valid IPs or CIDRs (not domains)
// - allowOut entries must be valid IPs, CIDRs, or domain names
// - when allowOut contains domains, denyOut must include 0.0.0.0/0
func validateEgressRules(allowOut, denyOut []string) *api.APIError {
	for _, cidr := range denyOut {
		if !sandbox_network.IsIPOrCIDR(cidr) {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid denied CIDR %s", cidr),
				ClientMsg: fmt.Sprintf("invalid denied CIDR %s", cidr),
			}
		}
	}

	if len(allowOut) > 0 {
		_, allowedDomains := sandbox_network.ParseAddressesAndDomains(allowOut)

		for _, domain := range allowedDomains {
			// Strip wildcard prefix for IDNA validation (*.example.com -> example.com).
			// The "*" label is not a valid IDNA label, but we support it as a wildcard.
			validateDomain := domain
			if strings.HasPrefix(domain, "*.") {
				validateDomain = domain[2:]
			}

			if validateDomain != "*" {
				if _, err := idna.Lookup.ToASCII(validateDomain); err != nil {
					return &api.APIError{
						Code:      http.StatusBadRequest,
						Err:       fmt.Errorf("invalid allowed domain %q: %w", domain, err),
						ClientMsg: fmt.Sprintf("invalid allowed domain: %s", domain),
					}
				}
			}
		}

		hasBlockAll := slices.Contains(denyOut, sandbox_network.AllInternetTrafficCIDR)

		if len(allowedDomains) > 0 && !hasBlockAll {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("allow out contains domains but deny out is missing 0.0.0.0/0 (ALL_TRAFFIC)"),
				ClientMsg: ErrMsgDomainsRequireBlockAll,
			}
		}
	}

	return nil
}

func validateIngressRules(ingress *types.SandboxNetworkIngressConfig) *api.APIError {
	if apiErr := validatePortList(ingress.AllowedPorts, "allowPorts"); apiErr != nil {
		return apiErr
	}

	if apiErr := validatePortList(ingress.DeniedPorts, "denyPorts"); apiErr != nil {
		return apiErr
	}

	if apiErr := validateCIDRList(ingress.AllowedClientCIDRs, "allowIn"); apiErr != nil {
		return apiErr
	}

	return validateCIDRList(ingress.DeniedClientCIDRs, "denyIn")
}

func validatePortList(ports []uint32, fieldName string) *api.APIError {
	for _, p := range ports {
		if p == 0 || p > 65535 {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid %s port %d", fieldName, p),
				ClientMsg: fmt.Sprintf("invalid %s port %d: must be between 1 and 65535", fieldName, p),
			}
		}
	}

	return nil
}

func validateCIDRList(cidrs []string, fieldName string) *api.APIError {
	for _, cidr := range cidrs {
		if !sandbox_network.IsIPOrCIDR(cidr) {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid %s CIDR %s", fieldName, cidr),
				ClientMsg: fmt.Sprintf("invalid %s CIDR %s", fieldName, cidr),
			}
		}
	}

	return nil
}
