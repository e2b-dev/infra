package catalog

import (
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	sandboxcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func KeepaliveFromDB(keepalive *types.SandboxKeepaliveConfig) *sandboxcatalog.Keepalive {
	if keepalive == nil {
		return nil
	}

	result := &sandboxcatalog.Keepalive{}
	if keepalive.Traffic != nil && keepalive.Traffic.Enabled {
		result.Traffic = &sandboxcatalog.TrafficKeepalive{
			Enabled: true,
		}
	}

	return result
}
