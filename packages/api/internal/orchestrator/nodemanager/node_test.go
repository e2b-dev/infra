package nodemanager

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func TestNode_OptimisticAdd(t *testing.T) {
	t.Parallel()

	node := NewTestNode("test-node", api.NodeStatusReady, 0, 4)
	initialMetrics := node.Metrics()

	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticAdd(res)

	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated+uint32(res.CPUs), newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes+uint64(res.MiBMemory)*1024*1024, newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticRemove(t *testing.T) {
	t.Parallel()

	// Node with resources already allocated at initialization
	node := NewTestNode("test-node", api.NodeStatusReady, 4, 8192, WithAllocatedMemoryBytes(8192*1024*1024))
	initialMetrics := node.Metrics()

	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticRemove(t.Context(), res)

	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated-uint32(res.CPUs), newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes-uint64(res.MiBMemory)*1024*1024, newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticRemove_SkipsWhenItWouldUnderflow(t *testing.T) {
	t.Parallel()

	// Node with less allocated than what will be removed: 1 CPU, 512 MiB
	node := NewTestNode("test-node", api.NodeStatusReady, 1, 8192, WithAllocatedMemoryBytes(512*1024*1024))
	initialMetrics := node.Metrics()

	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticRemove(t.Context(), res)

	// Counters must never wrap to ~2^32/2^64; subtraction is skipped instead
	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated, newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes, newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticRemove_FreshNodeDoesNotUnderflow(t *testing.T) {
	t.Parallel()

	// Fresh node: nothing allocated yet (e.g. poll overwrote counters after sandbox already left the orchestrator)
	node := NewTestNode("test-node", api.NodeStatusReady, 0, 8192)

	node.OptimisticRemove(t.Context(), SandboxResources{CPUs: 2, MiBMemory: 1024})

	newMetrics := node.Metrics()
	assert.Equal(t, uint32(0), newMetrics.CpuAllocated)
	assert.Equal(t, uint64(0), newMetrics.MemoryAllocatedBytes)
}
