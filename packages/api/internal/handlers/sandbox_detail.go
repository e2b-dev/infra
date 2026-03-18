package handlers

import (
	"slices"

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
			allowed := slices.Clone(egress.AllowedAddresses)
			result.AllowOut = &allowed
		}

		if egress.DeniedAddresses != nil {
			denied := slices.Clone(egress.DeniedAddresses)
			result.DenyOut = &denied
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
