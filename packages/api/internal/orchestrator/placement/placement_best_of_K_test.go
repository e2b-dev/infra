package placement

import (
	"fmt"
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

func TestBestOfK_Score_WithPendingResources(t *testing.T) {
	t.Parallel()
	config := DefaultBestOfKConfig()
	algo := NewBestOfK(config).(*BestOfK)

	// Create two nodes with identical base loads
	nodeNormal := nodemanager.NewTestNode("node-normal", api.NodeStatusReady, 0, 4)
	nodeWithPending := nodemanager.NewTestNode("node-pending", api.NodeStatusReady, 0, 4)

	// Inject InProgress resources into nodeWithPending using StartPlacing
	// This simulates a Sandbox that is currently being placed but hasn't fully started
	pendingRes := nodemanager.SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	nodeWithPending.PlacementMetrics.StartPlacing("pending-sbx-1", pendingRes)

	reqResources := nodemanager.SandboxResources{
		CPUs:      1,
		MiBMemory: 512,
	}

	scoreNormal := algo.Score(nodeNormal, reqResources, config)
	scorePending := algo.Score(nodeWithPending, reqResources, config)

	// A node with pending resources has a higher 'reserved' CPU count,
	// so its calculated Score should be greater (meaning worse/lower priority)
	assert.Greater(t, scorePending, scoreNormal, "Node with pending resources should receive a higher (worse) score")
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
	selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{}, false, nil)
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

	selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{}, false, nil)
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

	selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{}, false, nil)
	require.Error(t, err)
	assert.Nil(t, selected)
	assert.Contains(t, err.Error(), "no node available")
}

func TestFailedToPlaceSandboxError_Error(t *testing.T) {
	t.Parallel()

	machine := machineinfo.MachineInfo{
		CPUArchitecture: "x86_64",
		CPUFamily:       "6",
		CPUModel:        machineinfo.IceLakeModel,
		CPUModelName:    "Intel Ice Lake",
	}

	tests := []struct {
		name           string
		filterByLabels bool
		requiredLabels []string
		wantContains   []string
		wantNotContain string
	}{
		{
			name:           "without label filtering",
			filterByLabels: false,
			requiredLabels: nil,
			wantContains: []string{
				"no node available with required metadata",
				fmt.Sprintf("machine=%v", machine),
			},
			wantNotContain: "labels=",
		},
		{
			name:           "with label filtering",
			filterByLabels: true,
			requiredLabels: []string{"gpu", "fast-disk"},
			wantContains: []string{
				"no node available with required metadata",
				fmt.Sprintf("machine=%v", machine),
				"labels=[gpu fast-disk]",
			},
		},
		{
			name:           "label filtering enabled with no labels",
			filterByLabels: true,
			requiredLabels: nil,
			wantContains: []string{
				"no node available with required metadata",
				"labels=[]",
			},
		},
		{
			name:           "labels present but filtering disabled",
			filterByLabels: false,
			requiredLabels: []string{"gpu"},
			wantContains: []string{
				"no node available with required metadata",
			},
			wantNotContain: "labels=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := FailedToPlaceSandboxError{
				filterByLabels:   tt.filterByLabels,
				requiredLabels:   tt.requiredLabels,
				buildMachineInfo: machine,
			}

			msg := err.Error()

			for _, want := range tt.wantContains {
				assert.Contains(t, msg, want)
			}

			if tt.wantNotContain != "" {
				assert.NotContains(t, msg, tt.wantNotContain)
			}
		})
	}
}

func TestFailedToPlaceSandboxError_IsError(t *testing.T) {
	t.Parallel()

	// The error returned by chooseNode when no node is available should be a
	// FailedToPlaceSandboxError carrying the placement constraints.
	var err error = FailedToPlaceSandboxError{
		filterByLabels:   true,
		requiredLabels:   []string{"gpu"},
		buildMachineInfo: machineinfo.MachineInfo{CPUModelName: "Intel Ice Lake"},
	}

	var placeErr FailedToPlaceSandboxError
	require.ErrorAs(t, err, &placeErr)
	assert.True(t, placeErr.filterByLabels)
	assert.Equal(t, []string{"gpu"}, placeErr.requiredLabels)
	assert.Equal(t, "Intel Ice Lake", placeErr.buildMachineInfo.CPUModelName)
}

func TestBestOfK_ChooseNode_ReturnsPlacementError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	config := DefaultBestOfKConfig()
	algo := NewBestOfK(config).(*BestOfK)

	// Unhealthy nodes cannot be placed on, forcing the error path.
	node := nodemanager.NewTestNode("node1", api.NodeStatusUnhealthy, 2, 4)
	nodes := []*nodemanager.Node{node}
	resources := nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512}
	machine := machineinfo.MachineInfo{CPUModelName: "Intel Ice Lake"}
	requiredLabels := []string{"gpu"}

	selected, err := algo.chooseNode(ctx, nodes, make(map[string]struct{}), resources, machine, true, requiredLabels)
	require.Error(t, err)
	assert.Nil(t, selected)

	// The error should be a FailedToPlaceSandboxError with the constraints that
	// were requested, and its message should surface them.
	var placeErr FailedToPlaceSandboxError
	require.ErrorAs(t, err, &placeErr)
	assert.Equal(t, requiredLabels, placeErr.requiredLabels)
	assert.Contains(t, err.Error(), "labels=[gpu]")
	assert.Contains(t, err.Error(), fmt.Sprintf("machine=%v", machine))
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
	sampled := algo.sample(nodes, config, excludedNodes, resources, machineinfo.MachineInfo{}, false, nil)
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
	sampled = algo.sample(nodes, config, excludedNodes, resources, machineinfo.MachineInfo{}, false, nil)

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
		selected, err := algo.chooseNode(ctx, nodes, excludedNodes, resources, machineinfo.MachineInfo{}, false, nil)
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
