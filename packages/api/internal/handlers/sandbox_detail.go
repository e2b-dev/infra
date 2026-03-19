package handlers

import (
	"github.com/e2b-dev/infra/packages/api/internal/api"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

func toSandboxDetailNetworkConfig(network *dbtypes.SandboxNetworkConfig) api.SandboxNetworkConfig {
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

func toSandboxDetailLifecycle(autoResume *dbtypes.SandboxAutoResumeConfig, autoPause bool) api.SandboxLifecycle {
	onTimeout := api.Kill
	if autoPause {
		onTimeout = api.Pause
	}

	return api.SandboxLifecycle{
		AutoResume: autoResume != nil && autoResume.Policy == dbtypes.SandboxAutoResumeAny,
		OnTimeout:  onTimeout,
	}
}
