package catalog

import (
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	sandboxroutingcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func KeepaliveFromDB(keepalive *types.SandboxKeepaliveConfig) *sandboxroutingcatalog.Keepalive {
	if keepalive == nil {
		return nil
	}

	result := &sandboxroutingcatalog.Keepalive{}
	if keepalive.Traffic != nil && keepalive.Traffic.Enabled {
		result.Traffic = &sandboxroutingcatalog.TrafficKeepalive{
			Enabled: true,
		}
	}

	return result
}
