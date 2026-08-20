package placement

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

const gib = uint64(1024 * 1024 * 1024)

func TestBestOfK_MemoryFilter_Disabled_WhenMIsZero(t *testing.T) {
	t.Parallel()

	// Node has only 1 GiB total but is allocated 900 MiB; sandbox requests 512 MiB.
	// Without a memory filter (M=0) this node should still be eligible.
	node := nodemanager.NewTestNode("node-a", api.NodeStatusReady, 0, 8,
		nodemanager.WithTotalMemoryBytes(gib),
		nodemanager.WithAllocatedMemoryBytes(900*1024*1024),
	)

	config := BestOfKConfig{R: 4, K: 10, Alpha: 0.5, M: 0}
	algo := NewBestOfK(config).(*BestOfK)

	resources := nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512}
	candidates := algo.sample([]*nodemanager.Node{node}, config, resources, map[string]struct{}{}, machineinfo.MachineInfo{}, false, nil)
	require.Len(t, candidates, 1, "memory filter must be inactive when M=0")
}

func TestBestOfK_MemoryFilter_ExcludesFullNode(t *testing.T) {
	t.Parallel()

	// Node has 2 GiB total, 1.9 GiB allocated; sandbox requests 512 MiB.
	// With M=1.0 (no overcommit), 1.9 GiB + 0.5 GiB = 2.4 GiB > 2 GiB → excluded.
	node := nodemanager.NewTestNode("node-full", api.NodeStatusReady, 0, 8,
		nodemanager.WithTotalMemoryBytes(2*gib),
		nodemanager.WithAllocatedMemoryBytes(1900 * 1024 * 1024),
	)

	config := BestOfKConfig{R: 4, K: 10, Alpha: 0.5, M: 1.0}
	algo := NewBestOfK(config).(*BestOfK)

	resources := nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512}
	candidates := algo.sample([]*nodemanager.Node{node}, config, resources, map[string]struct{}{}, machineinfo.MachineInfo{}, false, nil)
	assert.Empty(t, candidates, "full node must be excluded when M=1.0")
}

func TestBestOfK_MemoryFilter_AllowsNodeWithHeadroom(t *testing.T) {
	t.Parallel()

	// Node has 4 GiB total, 1 GiB allocated; sandbox requests 512 MiB.
	// 1 GiB + 0.5 GiB = 1.5 GiB < 4 GiB → eligible.
	node := nodemanager.NewTestNode("node-ok", api.NodeStatusReady, 0, 8,
		nodemanager.WithTotalMemoryBytes(4*gib),
		nodemanager.WithAllocatedMemoryBytes(gib),
	)

	config := BestOfKConfig{R: 4, K: 10, Alpha: 0.5, M: 1.0}
	algo := NewBestOfK(config).(*BestOfK)

	resources := nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512}
	candidates := algo.sample([]*nodemanager.Node{node}, config, resources, map[string]struct{}{}, machineinfo.MachineInfo{}, false, nil)
	require.Len(t, candidates, 1, "node with headroom must be included")
}

func TestBestOfK_MemoryFilter_OvercommitAllowsExcess(t *testing.T) {
	t.Parallel()

	// Node has 2 GiB total, 1.9 GiB allocated; sandbox requests 512 MiB.
	// With M=1.5 (50% overcommit), limit = 3 GiB; 1.9 + 0.5 = 2.4 GiB < 3 GiB → eligible.
	node := nodemanager.NewTestNode("node-overcommit", api.NodeStatusReady, 0, 8,
		nodemanager.WithTotalMemoryBytes(2*gib),
		nodemanager.WithAllocatedMemoryBytes(1900 * 1024 * 1024),
	)

	config := BestOfKConfig{R: 4, K: 10, Alpha: 0.5, M: 1.5}
	algo := NewBestOfK(config).(*BestOfK)

	resources := nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512}
	candidates := algo.sample([]*nodemanager.Node{node}, config, resources, map[string]struct{}{}, machineinfo.MachineInfo{}, false, nil)
	require.Len(t, candidates, 1, "overcommit ratio M=1.5 must allow this allocation")
}

func TestBestOfK_MemoryFilter_SkipsWhenTotalBytesZero(t *testing.T) {
	t.Parallel()

	// Node has not yet reported MemoryTotalBytes (zero); filter should be skipped.
	node := nodemanager.NewTestNode("node-unreported", api.NodeStatusReady, 0, 8)

	config := BestOfKConfig{R: 4, K: 10, Alpha: 0.5, M: 1.0}
	algo := NewBestOfK(config).(*BestOfK)

	resources := nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512}
	candidates := algo.sample([]*nodemanager.Node{node}, config, resources, map[string]struct{}{}, machineinfo.MachineInfo{}, false, nil)
	require.Len(t, candidates, 1, "filter must be skipped when MemoryTotalBytes=0")
}

func TestBestOfK_MemoryFilter_SelectsNodeWithMoreHeadroom(t *testing.T) {
	t.Parallel()

	// Two nodes: tight has 200 MiB free, spacious has 2 GiB free.
	// K=10 samples all nodes; Score() is CPU-only, so both are eligible.
	// The memory filter only hard-excludes; this test verifies neither is excluded.
	tight := nodemanager.NewTestNode("tight", api.NodeStatusReady, 4, 8,
		nodemanager.WithTotalMemoryBytes(4*gib),
		nodemanager.WithAllocatedMemoryBytes(4*gib-200*1024*1024),
	)
	spacious := nodemanager.NewTestNode("spacious", api.NodeStatusReady, 0, 8,
		nodemanager.WithTotalMemoryBytes(4*gib),
		nodemanager.WithAllocatedMemoryBytes(2*gib),
	)

	config := BestOfKConfig{R: 4, K: 10, Alpha: 0.5, M: 1.0}
	algo := NewBestOfK(config).(*BestOfK)

	resources := nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512}
	// tight: 3.8 GiB + 0.5 GiB = 4.3 GiB > 4 GiB → excluded
	// spacious: 2 GiB + 0.5 GiB = 2.5 GiB < 4 GiB → eligible
	candidates := algo.sample([]*nodemanager.Node{tight, spacious}, config, resources, map[string]struct{}{}, machineinfo.MachineInfo{}, false, nil)
	require.Len(t, candidates, 1)
	assert.Equal(t, "spacious", candidates[0].ID)
}
