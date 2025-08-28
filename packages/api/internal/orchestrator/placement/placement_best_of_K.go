package placement

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

// BestOfKConfig holds the configuration parameters for the placement algorithm
type BestOfKConfig struct {
	// R is the cluster-wide max over-commit ratio
	R float64
	// Alpha is the weight for CPU usage in the score calculation
	Alpha float64
	// K is the number of candidate nodes sampled per placement ("power of K choices")
	K int
}

// DefaultBestOfKConfig returns the default placement configuration
func DefaultBestOfKConfig() BestOfKConfig {
	return BestOfKConfig{
		R:     4,
		K:     3,
		Alpha: 0.5,
	}
}

// Score calculates the placement score for this node
func (b *BestOfK) Score(node *nodemanager.Node, resources nodemanager.SandboxResources) float64 {
	metrics := node.Metrics()
	reserved := metrics.CpuAllocated

	// 1 CPU used = 100% CPU percept
	usageAvg := float64(metrics.CpuPercent) / 100

	// to avoid division by zero
	cpuCount := float64(metrics.CpuCount)
	if cpuCount == 0 {
		return math.MaxFloat64
	}

	totalCapacity := b.config.R * cpuCount

	cpuRequested := float64(resources.CPUs)

	return (cpuRequested + float64(reserved) + b.config.Alpha*usageAvg) / totalCapacity
}

// CanFit checks if the node can fit a new VM with the given quota
func (b *BestOfK) CanFit(node *nodemanager.Node, sandboxResources nodemanager.SandboxResources) bool {
	metrics := node.Metrics()

	reserved := metrics.CpuAllocated

	// If the node has no CPUs, there's probably a problem
	cpuCount := float64(metrics.CpuCount)
	if cpuCount == 0 {
		return false
	}

	totalCapacity := b.config.R * cpuCount

	return float64(reserved+uint32(sandboxResources.CPUs)) <= totalCapacity
}

// BestOfK implements the fit-score-place algorithm
type BestOfK struct {
	config BestOfKConfig
	mu     sync.RWMutex
}

var _ Algorithm = &BestOfK{}

// NewBestOfK creates a new placement algorithm with the given config
func NewBestOfK(config BestOfKConfig) Algorithm {
	return &BestOfK{
		config: config,
	}
}

func (b *BestOfK) getConfig() BestOfKConfig {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.config
}

// chooseNode selects the best node for placing a VM with the given quota
func (b *BestOfK) chooseNode(_ context.Context, nodes []*nodemanager.Node, excludedNodes map[string]struct{}, resources nodemanager.SandboxResources) (bestNode *nodemanager.Node, err error) {
	// Fix the config, we want to dynamically update it
	config := b.getConfig()

	// Filter eligible nodes
	candidates := b.sample(nodes, config.K, excludedNodes, resources)

	// Find the best node among candidates
	bestScore := math.MaxFloat64

	for _, node := range candidates {
		// Calculate score
		score := b.Score(node, resources)

		if score < bestScore {
			bestNode = node
			bestScore = score
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no node available")
	}

	return bestNode, nil
}

// sample returns up to k items chosen uniformly from those passing ok.
func (b *BestOfK) sample(items []*nodemanager.Node, k int, excludedNodes map[string]struct{}, resources nodemanager.SandboxResources) []*nodemanager.Node {
	if k <= 0 || len(items) == 0 {
		return nil
	}
	indices := make([]int, len(items))
	for i := range indices {
		indices[i] = i
	}

	candidates := make([]*nodemanager.Node, 0, k)
	remaining := len(indices) // active pool is indices[:remaining]

	for len(candidates) < k && remaining > 0 {
		// pick from the active pool
		j := rand.Intn(remaining)
		pick := indices[j]

		// remove j from pool
		indices[j], indices[remaining-1] = indices[remaining-1], indices[j]
		remaining--

		n := items[pick]

		// Excluded filter
		if _, ok := excludedNodes[n.ID]; ok {
			continue
		}

		// If the node is not ready, skip it
		if n.Status() != api.NodeStatusReady {
			continue
		}

		// Fit filter
		if !b.CanFit(n, resources) {
			continue
		}

		// To prevent overloading the node
		if n.PlacementMetrics.InProgressCount() > maxStartingInstancesPerNode {
			continue
		}

		candidates = append(candidates, n)
	}

	return candidates
}
