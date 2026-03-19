package handlers

import (
	"github.com/e2b-dev/infra/packages/api/internal/api"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

func dbNetworkConfigToAPI(network *dbtypes.SandboxNetworkConfig) api.SandboxNetworkConfig {
	result := api.SandboxNetworkConfig{}
	if network == nil {
		return result
	}

	if ingress := network.Ingress; ingress != nil {
		result.AllowPublicTraffic = ingress.AllowPublicAccess
		result.MaskRequestHost = ingress.MaskRequestHost
	}

	if egress := network.Egress; egress != nil {
		if egress.AllowedAddresses != nil {
			result.AllowOut = &egress.AllowedAddresses
		}
		if egress.DeniedAddresses != nil {
			result.DenyOut = &egress.DeniedAddresses
		}
	}

	return result
}
