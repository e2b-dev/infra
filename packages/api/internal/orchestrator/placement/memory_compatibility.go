package placement

import (
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

// MemoryRequirement bounds how much of a node's hugepage pool placement may
// commit.
type MemoryRequirement struct {
	// Limit is the share of the pool ceiling committed pages may reach; 0 turns
	// the check off.
	Limit float64
}

// NodeSatisfiesMemory reports whether node can take resources with its
// committed hugepages (used, reserved, in flight, requested) staying within
// memory.Limit of the pool ceiling. HugePages_Total is no ceiling: it is the
// base pool plus only the surplus already borrowed, so a node on surplus would
// always look full against it.
func NodeSatisfiesMemory(node *nodemanager.Node, resources nodemanager.SandboxResources, memory MemoryRequirement) bool {
	if !resources.HugePages || memory.Limit <= 0 {
		return true
	}

	// In flight before metrics: a placement finishing in between moves from
	// the first to the second, so it is counted at least once.
	inFlight := node.PlacementMetrics.InProgress()
	metrics := node.Metrics()
	if metrics.HugePageSizeBytes == 0 || metrics.HugePagesCapacity == 0 {
		return true
	}

	var pendingMiB int64
	for _, res := range inFlight {
		if res.HugePages {
			pendingMiB += res.MiBMemory
		}
	}

	committed := metrics.HugePagesUsed + metrics.HugePagesReserved +
		nodemanager.HugePagesFor(pendingMiB, metrics.HugePageSizeBytes) +
		nodemanager.HugePagesFor(resources.MiBMemory, metrics.HugePageSizeBytes)

	return float64(committed) <= memory.Limit*float64(metrics.HugePagesCapacity)
}
