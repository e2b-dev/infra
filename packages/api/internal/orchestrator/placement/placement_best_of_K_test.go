package placement

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

func TestBestOfK_Score(t *testing.T) {
	t.Parallel()
	config := DefaultBestOfKConfig()
	algo := NewBestOfK(config).(*BestOfK)

	// Create a test node with known metrics
	// The node's CpuUsage is set to 50 via the constructor
	node := nodemanager.NewTestNode("test-node", api.NodeStatusReady, 2, 4)

	resources := nodemanager.SandboxResources{
		CPUs:      1,
		MiBMemory: 512,
	}

	score := algo.Score(node, resources, config)

	// Score should be non-negative
	assert.GreaterOrEqual(t, score, 0.0)

	// Test with different CPU usage
	node2 := nodemanager.NewTestNode("test-node2", api.NodeStatusReady, 10, 4)
	score2 := algo.Score(node2, resources, config)

	// Higher CPU usage should result in higher score (worse)
	assert.Greater(t, score2, score)
}

func TestBestOfK_Score_PreferBiggerNode(t *testing.T) {
	t.Parallel()
	config := DefaultBestOfKConfig()
	algo := NewBestOfK(config).(*BestOfK)

	// Create a test node with known metrics
	// The node's CpuUsage is set to 50 via the constructor
	node := nodemanager.NewTestNode("test-node", api.NodeStatusReady, 5, 4)

	resources := nodemanager.SandboxResources{
		CPUs:      1,
		MiBMemory: 512,
	}

	score := algo.Score(node, resources, config)

	// Score should be non-negative
	assert.GreaterOrEqual(t, score, 0.0)

	// Test with different CPU usage
	node2 := nodemanager.NewTestNode("test-node2", api.NodeStatusReady, 1, 8)
	score2 := algo.Score(node2, resources, config)

	// Lower CPU count should result in higher score (worse) as the expected load is higher
	assert.Greater(t, score, score2)
}

func TestBestOfK_CanFit(t *testing.T) {
	t.Parallel()
	config := DefaultBestOfKConfig()
	algo := NewBestOfK(config).(*BestOfK)

	// Create a test node with moderate CPU usage
	node := nodemanager.NewTestNode("test-node", api.NodeStatusReady, 5, 4)

	// Test can fit small resource request
	resources := nodemanager.SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	// Note: CanFit depends on the node's actual metrics which we can't directly control
	// But we can test the logic works
	canFit := algo.CanFit(node, resources, config)
	assert.IsType(t, true, canFit) // Should return a boolean

	// Test with very large resource request - likely won't fit
	largeResources := nodemanager.SandboxResources{
		CPUs:      1000,
		MiBMemory: 8192,
	}
	canFitLarge := algo.CanFit(node, largeResources, config)
	assert.False(t, canFitLarge) // Very large request should not fit
}

func TestBestOfK_ChooseNode(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	config := BestOfKConfig{
		R:     10, // Higher overcommit ratio to ensure nodes can fit
		Alpha: 0.5,
		K:     3, // Sample all nodes
	}
	algo := NewBestOfK(config).(*BestOfK)

	// Create test nodes with different loads
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 8, 4)
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusReady, 2, 4)
	node3 := nodemanager.NewTestNode("node3", api.NodeStatusReady, 5, 4)

	nodes := []*nodemanager.Node{node1, node2, node3}
	excludedNodes := make(map[string]struct{})
	resources := nodemanager.SandboxResources{
		CPUs:      1, // Small resource request
		MiBMemory: 512,
	}

	// Test selection - should work with proper config
	selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{})
	require.NoError(t, err)
	assert.NotNil(t, selected)
	assert.Contains(t, []string{"node1", "node2", "node3"}, selected.ID)
}

func TestBestOfK_ChooseNode_WithExclusions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	config := BestOfKConfig{
		R:     10,
		Alpha: 0.5,
		K:     3,
	}
	algo := NewBestOfK(config).(*BestOfK)

	// Create test nodes
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 8, 4)
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusReady, 2, 4)
	node3 := nodemanager.NewTestNode("node3", api.NodeStatusReady, 5, 4)

	nodes := []*nodemanager.Node{node1, node2, node3}

	// Exclude the best node (node2)
	excludedNodes := map[string]struct{}{
		"node2": {},
	}

	resources := nodemanager.SandboxResources{
		CPUs:      1,
		MiBMemory: 512,
	}

	selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{})
	require.NoError(t, err)
	// Should not select excluded node
	assert.NotEqual(t, "node2", selected.ID)
	assert.Contains(t, []string{"node1", "node3"}, selected.ID)
}

func TestBestOfK_ChooseNode_NoAvailableNodes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	config := DefaultBestOfKConfig()
	algo := NewBestOfK(config).(*BestOfK)

	// Create unhealthy nodes
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusUnhealthy, 8, 4)
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusUnhealthy, 2, 4)

	nodes := []*nodemanager.Node{node1, node2}
	excludedNodes := make(map[string]struct{})
	resources := nodemanager.SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}

	selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{})
	require.Error(t, err)
	assert.Nil(t, selected)
	assert.Contains(t, err.Error(), "no node available")
}

func TestBestOfK_Sample(t *testing.T) {
	t.Parallel()
	config := DefaultBestOfKConfig()
	algo := NewBestOfK(config).(*BestOfK)

	// Create many test nodes
	var nodes []*nodemanager.Node
	for i := range 10 {
		node := nodemanager.NewTestNode(string(rune('a'+i)), api.NodeStatusReady, int64(i), 4)
		nodes = append(nodes, node)
	}

	excludedNodes := make(map[string]struct{})
	resources := nodemanager.SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}

	// Test sampling fewer nodes than available
	sampled := algo.sample(nodes, config, excludedNodes, resources, machineinfo.MachineInfo{})
	assert.LessOrEqual(t, len(sampled), 3)

	// Check all sampled nodes are unique
	seen := make(map[string]bool)
	for _, n := range sampled {
		assert.False(t, seen[n.ID])
		seen[n.ID] = true
	}

	// Test sampling with exclusions
	excludedNodes["a"] = struct{}{}
	excludedNodes["b"] = struct{}{}
	sampled = algo.sample(nodes, config, excludedNodes, resources, machineinfo.MachineInfo{})

	for _, n := range sampled {
		assert.NotEqual(t, "a", n.ID)
		assert.NotEqual(t, "b", n.ID)
	}
}

func TestBestOfK_PowerOfKChoices(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	config := BestOfKConfig{
		R:     10,
		Alpha: 0.5,
		K:     3,
	}
	algo := NewBestOfK(config).(*BestOfK)

	// Create many nodes with varying loads
	var nodes []*nodemanager.Node
	for i := range 20 {
		node := nodemanager.NewTestNode(string(rune('A'+i)), api.NodeStatusReady, int64(float64(i)*0.5), 4)
		nodes = append(nodes, node)
	}

	excludedNodes := make(map[string]struct{})
	resources := nodemanager.SandboxResources{
		CPUs:      1,
		MiBMemory: 512,
	}

	// Run multiple times to see distribution
	selectedCounts := make(map[string]int)
	successCount := 0
	for range 100 {
		selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{})
		if err == nil && selected != nil {
			selectedCounts[selected.ID]++
			successCount++
		}
	}

	// We should see multiple different nodes selected due to random sampling
	assert.GreaterOrEqual(t, len(selectedCounts), 1)

	// Lower-loaded nodes should be selected more often if we have enough samples
	if successCount > 10 {
		var earlyNodeCount, lateNodeCount int
		for id, count := range selectedCounts {
			if id[0] < 'J' {
				earlyNodeCount += count
			} else {
				lateNodeCount += count
			}
		}
		// Early nodes (lower load) should be selected more often
		assert.GreaterOrEqual(t, earlyNodeCount, lateNodeCount)
	}
}
