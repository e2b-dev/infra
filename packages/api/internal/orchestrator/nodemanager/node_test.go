package nodemanager_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

func TestNode_OptimisticAdd(t *testing.T) {
	t.Parallel()
	
	// Initialize an idle node
	node := nodemanager.NewTestNode("test-node", api.NodeStatusReady, 0, 4)
	initialMetrics := node.Metrics()

	// Simulate the resources to be allocated
	res := nodemanager.SandboxResources{
		CPUs:      2,
		MiBMemory: 1024, // 1GB
	}

	// Perform optimistic addition
	node.OptimisticAdd(res)
	newMetrics := node.Metrics()

	// Verify that CPU and memory are increased as expected
	assert.Equal(t, initialMetrics.CpuAllocated+uint32(res.CPUs), newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes+uint64(res.MiBMemory)*1024*1024, newMetrics.MemoryAllocatedBytes)
}