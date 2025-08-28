package placement

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

// createTestNode creates a test Node for testing
func createTestNode(id string, status api.NodeStatus, cpuAllocated int64, inProgressCount uint32) *nodemanager.TestNode {
	node := nodemanager.NewTestNode(id, status, cpuAllocated, 4)

	// Add sandboxes to the placement metrics
	for i := uint32(0); i < inProgressCount; i++ {
		node.PlacementMetrics.StartPlacing(fmt.Sprintf("sandbox-%d", i), nodemanager.SandboxResources{
			CPUs:      1,
			MiBMemory: 512,
		})
	}

	return node
}

func TestLeastBusyAlgorithm_FindLeastBusyNode_Basic(t *testing.T) {
	algorithm := &LeastBusyAlgorithm{}

	// Create test nodes with known states
	nodes := []*nodemanager.TestNode{
		createTestNode("node1", api.NodeStatusReady, 8, 0),
		createTestNode("node2", api.NodeStatusReady, 2, 0),
		createTestNode("node3", api.NodeStatusReady, 5, 0),
	}

	excludedNodes := make(map[string]struct{})

	// Use the test-safe version of findLeastBusyNode
	selectedNode, err := algorithm.findLeastBusyNode(nodes, excludedNodes)

	assert.NoError(t, err)
	assert.NotNil(t, selectedNode)
	// Should select node2 as it has the lowest CPU usage
	assert.Equal(t, "node2", selectedNode.ID)
}

func TestLeastBusyAlgorithm_FindLeastBusyNode_ExcludesNodes(t *testing.T) {
	algorithm := &LeastBusyAlgorithm{}

	nodes := []*nodemanager.Node{
		createTestNode("node1", api.NodeStatusReady, 8, 0),
		createTestNode("node2", api.NodeStatusReady, 2, 0),
		createTestNode("node3", api.NodeStatusReady, 5, 0),
	}

	excludedNodes := map[string]struct{}{
		"node1": {},
		"node2": {},
	}

	selectedNode, err := algorithm.findLeastBusyNode(nodes, excludedNodes)

	assert.NoError(t, err)
	assert.NotNil(t, selectedNode)
	assert.Equal(t, "node3", selectedNode.ID)
}

func TestLeastBusyAlgorithm_FindLeastBusyNode_NoAvailableNodes(t *testing.T) {
	algorithm := &LeastBusyAlgorithm{}

	// Create all unhealthy nodes
	nodes := []*nodemanager.Node{
		createTestNode("node1", api.NodeStatusUnhealthy, 8, 0),
		createTestNode("node2", api.NodeStatusUnhealthy, 2, 0),
		createTestNode("node3", api.NodeStatusUnhealthy, 5, 0),
	}

	excludedNodes := make(map[string]struct{})

	selectedNode, err := algorithm.findLeastBusyNode(nodes, excludedNodes)

	assert.Error(t, err)
	assert.Nil(t, selectedNode)
	assert.Contains(t, err.Error(), "no node available")
}

func TestLeastBusyAlgorithm_FindLeastBusyNode_HandlesNilNodes(t *testing.T) {
	algorithm := &LeastBusyAlgorithm{}

	nodes := []*nodemanager.Node{
		createTestNode("node1", api.NodeStatusReady, 9, 0),
		nil, // Nil node in the list
		createTestNode("node3", api.NodeStatusReady, 5, 0),
	}

	excludedNodes := make(map[string]struct{})

	selectedNode, err := algorithm.findLeastBusyNode(nodes, excludedNodes)

	assert.NoError(t, err)
	assert.NotNil(t, selectedNode)
	// Should select node3 as it has lower CPU usage than node1
	assert.Equal(t, "node3", selectedNode.ID)
}

func TestLeastBusyAlgorithm_FindLeastBusyNode_SkipsOverloadedNodes(t *testing.T) {
	algorithm := &LeastBusyAlgorithm{}

	// Create nodes with different in-progress counts
	node1 := createTestNode("node1", api.NodeStatusReady, 8, 0)
	node2 := createTestNode("node2", api.NodeStatusReady, 2, maxStartingInstancesPerNode+1)
	node3 := createTestNode("node3", api.NodeStatusReady, 5, 0)

	nodes := []*nodemanager.Node{node1, node2, node3}
	excludedNodes := make(map[string]struct{})

	selectedNode, err := algorithm.findLeastBusyNode(nodes, excludedNodes)

	assert.NoError(t, err)
	assert.NotNil(t, selectedNode)
	// node2 should be skipped due to too many in-progress instances
	// Should select node3 as it has lower CPU usage than node1
	assert.Equal(t, "node3", selectedNode.ID)
}

func TestLeastBusyAlgorithm_ChooseNode_ContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	algorithm := &LeastBusyAlgorithm{}

	// Create all unhealthy nodes to force waiting
	nodes := []*nodemanager.Node{
		createTestNode("node1", api.NodeStatusUnhealthy, 8, 0),
		createTestNode("node2", api.NodeStatusUnhealthy, 2, 0),
	}

	excludedNodes := make(map[string]struct{})
	requested := nodemanager.SandboxResources{CPUs: 2, MiBMemory: 1024}

	selectedNode, err := algorithm.chooseNode(ctx, nodes, excludedNodes, requested)

	assert.Error(t, err)
	assert.Nil(t, selectedNode)
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestLeastBusyAlgorithm_FindLeastBusyNode_EmptyNodesList(t *testing.T) {
	algorithm := &LeastBusyAlgorithm{}

	excludedNodes := make(map[string]struct{})

	selectedNode, err := algorithm.findLeastBusyNode([]*nodemanager.Node{}, excludedNodes)

	assert.Error(t, err)
	assert.Nil(t, selectedNode)
	assert.Contains(t, err.Error(), "no node available")
}

func TestLeastBusyAlgorithm_FindLeastBusyNode_AllNodesExcluded(t *testing.T) {
	algorithm := &LeastBusyAlgorithm{}

	nodes := []*nodemanager.Node{
		createTestNode("node1", api.NodeStatusReady, 8, 0),
		createTestNode("node2", api.NodeStatusReady, 2, 0),
		createTestNode("node3", api.NodeStatusReady, 5, 0),
	}

	excludedNodes := map[string]struct{}{
		"node1": {},
		"node2": {},
		"node3": {},
	}

	selectedNode, err := algorithm.findLeastBusyNode(nodes, excludedNodes)

	assert.Error(t, err)
	assert.Nil(t, selectedNode)
	assert.Contains(t, err.Error(), "no node available")
}
