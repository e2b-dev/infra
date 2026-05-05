package catalog

import (
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func KeepaliveFromDB(keepalive *types.SandboxKeepaliveConfig) *e2bcatalog.Keepalive {
	if keepalive == nil {
		return nil
	}

	result := &e2bcatalog.Keepalive{}
	if keepalive.Traffic != nil && keepalive.Traffic.Enabled {
		result.Traffic = &e2bcatalog.TrafficKeepalive{
			Enabled: true,
		}
	}

	return result
}
