package consul

import (
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const shortNodeIDLength = 8

var (
	getNodeID = sync.OnceValue(func() string {
		return utils.RequiredEnv("NODE_ID", "Nomad ID of the instance node")

	})
	// Node ID must be at least 8 characters long.
	GetClientID = sync.OnceValue(func() string {
		return getNodeID()[:shortNodeIDLength]
	})
)
