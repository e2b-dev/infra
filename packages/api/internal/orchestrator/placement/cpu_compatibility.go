package placement

import (
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

// isNodeCPUCompatible checks if a single node is compatible with the build CPU requirements.
// Returns true if:
// - Build has no CPU requirements (empty architecture)
// - Node's CPU info matches the build's requirements
func isNodeCPUCompatible(node *nodemanager.Node, buildMachineInfo machineinfo.MachineInfo) bool {
	// If build has no machine info, all nodes are compatible (backward compatibility)
	if buildMachineInfo.CPUArchitecture == "" {
		return true
	}

	nodeMachineInfo := node.MachineInfo()

	return buildMachineInfo.IsCompatibleWith(nodeMachineInfo)
}
