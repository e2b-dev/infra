package consul

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const shortNodeIDLength = 8

func GetClientID() string {
	nodeID := utils.RequiredEnv("NODE_ID", "Nomad ID of the instance node")

	return nodeID[:shortNodeIDLength]
}
