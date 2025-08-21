package placement

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

const maxRetries = 3

type LeastBusyAlgorithm struct{}

var _ Algorithm = &LeastBusyAlgorithm{}

// ChooseNode returns the least busy node, if there are no eligible nodes, it tries until one is available or the context timeouts
func (a *LeastBusyAlgorithm) chooseNode(ctx context.Context, nodes []*nodemanager.Node, nodesExcluded map[string]struct{}, _ nodemanager.SandboxResources) (leastBusyNode *nodemanager.Node, err error) {
	ctx, cancel := context.WithTimeout(ctx, leastBusyNodeTimeout)
	defer cancel()

	// Try to find a node without waiting
	leastBusyNode, err = a.findLeastBusyNode(nodes, nodesExcluded)
	if err == nil {
		return leastBusyNode, nil
	}

	// If no node is available, wait for a bit and try again
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			// If no node is available, wait for a bit and try again
			leastBusyNode, err = a.findLeastBusyNode(nodes, nodesExcluded)
			if err == nil {
				return leastBusyNode, nil
			}
		}
	}
}

// findLeastBusyNode finds the least busy node that is ready and not in the excluded list
// if no node is available, returns an error
func (a *LeastBusyAlgorithm) findLeastBusyNode(clusterNodes []*nodemanager.Node, nodesExcluded map[string]struct{}) (leastBusyNode *nodemanager.Node, err error) {
	for _, node := range clusterNodes {
		// The node might be nil if it was removed from the list while iterating
		if node == nil {
			continue
		}

		// If the node is not ready, skip it
		if node.Status() != api.NodeStatusReady {
			continue
		}

		// Skip already tried clusterNodes
		if _, ok := nodesExcluded[node.ID]; ok {
			continue
		}

		// To prevent overloading the node
		if node.PlacementMetrics.InProgressCount() > maxStartingInstancesPerNode {
			continue
		}

		cpuUsage := int64(0)
		for _, sbx := range node.PlacementMetrics.InProgress() {
			cpuUsage += sbx.CPUs
		}

		metrics := node.Metrics()

		if leastBusyNode == nil || (metrics.CpuUsage)+cpuUsage < leastBusyNode.Metrics().CpuUsage {
			leastBusyNode = node
		}
	}

	if leastBusyNode != nil {
		return leastBusyNode, nil
	}

	return nil, fmt.Errorf("no node available")
}
