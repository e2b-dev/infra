package placement

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

// BestOfKConfig holds the configuration parameters for the placement algorithm
type BestOfKConfig struct {
	// R is the cluster-wide max over-commit ratio
	R float64
	// Alpha is the weight for CPU usage in the score calculation
	Alpha float64
	// K is the number of candidate nodes sampled per placement ("power of K choices")
	K int
	// TooManyStarting determines whether to skip nodes that are starting more than maxStartingInstancesPerNode instances
	TooManyStarting bool
	// CanFit determines whether to skip the node CanFit check
	CanFit bool
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
func (b *BestOfK) Score(node *nodemanager.Node, resources nodemanager.SandboxResources, config BestOfKConfig, affinityScores ...map[string]float64) float64 {
	metrics := node.Metrics()

	// Get locally recorded resources that haven't been reported yet.
	pendingCPUs := int64(0)
	for _, res := range node.PlacementMetrics.InProgress() {
		pendingCPUs += res.CPUs
	}

	// Combine allocated resources with in-progress allocations
	reserved := metrics.CpuAllocated + uint32(pendingCPUs)

	// 1 CPU used = 100% CPU percept
	usageAvg := float64(metrics.CpuPercent) / 100

	// to avoid division by zero
	cpuCount := float64(metrics.CpuCount)
	if cpuCount == 0 {
		return math.MaxFloat64
	}

	totalCapacity := config.R * cpuCount

	cpuRequested := float64(resources.CPUs)

	score := (cpuRequested + float64(reserved) + config.Alpha*usageAvg) / totalCapacity
	if len(affinityScores) > 0 {
		score -= affinityScores[0][node.ID] / totalCapacity
	}

	return score
}

// CanFit checks if the node can fit a new VM with the given quota
func (b *BestOfK) CanFit(node *nodemanager.Node, sandboxResources nodemanager.SandboxResources, config BestOfKConfig) bool {
	metrics := node.Metrics()

	reserved := metrics.CpuAllocated

	// If the node has no CPUs, there's probably a problem
	cpuCount := float64(metrics.CpuCount)
	if cpuCount == 0 {
		return false
	}

	totalCapacity := config.R * cpuCount

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

// UpdateConfig updates the BestOfK algorithm configuration
func (b *BestOfK) UpdateConfig(config BestOfKConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config = config
}

// chooseNode selects the best node for placing a VM with the given quota
func (b *BestOfK) chooseNode(_ context.Context, nodes []*nodemanager.Node, excludedNodes map[string]struct{}, resources nodemanager.SandboxResources, buildMachineInfo machineinfo.MachineInfo, filterByLabels bool, requiredLabels []string, affinityScores ...map[string]float64) (bestNode *nodemanager.Node, err error) {
	// Fix the config, we want to dynamically update it
	config := b.getConfig()
	var affinity map[string]float64
	if len(affinityScores) > 0 {
		affinity = affinityScores[0]
	}

	// Filter eligible nodes
	candidates := b.sample(nodes, config, excludedNodes, resources, buildMachineInfo, filterByLabels, requiredLabels)
	if len(affinity) > 0 {
		seen := make(map[string]struct{}, len(candidates))
		for _, n := range candidates {
			seen[n.ID] = struct{}{}
		}
		for _, n := range nodes {
			if affinity[n.ID] <= 0 {
				continue
			}
			if _, ok := seen[n.ID]; ok {
				continue
			}
			if b.isCandidate(n, config, excludedNodes, resources, buildMachineInfo, filterByLabels, requiredLabels) {
				candidates = append(candidates, n)
			}
		}
	}

	// Find the best node among candidates
	bestScore := math.MaxFloat64

	for _, node := range candidates {
		// Calculate score
		score := b.Score(node, resources, config, affinity)

		if score < bestScore {
			bestNode = node
			bestScore = score
		}
	}

	if bestNode == nil {
		return nil, errors.New("no node available")
	}

	return bestNode, nil
}

func (b *BestOfK) isCandidate(node *nodemanager.Node, config BestOfKConfig, excludedNodes map[string]struct{}, resources nodemanager.SandboxResources, buildMachineInfo machineinfo.MachineInfo, filterByLabels bool, requiredLabels []string) bool {
	if _, ok := excludedNodes[node.ID]; ok {
		return false
	}
	// Local nodes are synthetic and may not report the full production status/label set.
	if env.IsLocal() && node.ClusterID == consts.LocalClusterID {
		return true
	}
	// If the node is not ready, we don't want to schedule a new sandbox on it.
	if node.Status() != api.NodeStatusReady {
		return false
	}
	// If the node CPU doesn't match the requested machine, we skip it.
	if !isNodeCPUCompatible(node, buildMachineInfo) {
		return false
	}
	// If label filtering is enabled, the node has to match the team's required labels.
	if filterByLabels && !isNodeLabelsCompatible(node, requiredLabels) {
		return false
	}
	// If can-fit is enabled, the node must have enough capacity for the requested resources.
	if config.CanFit && !b.CanFit(node, resources, config) {
		return false
	}
	// Avoid placing on nodes that already have too many sandboxes starting.
	if config.TooManyStarting && node.PlacementMetrics.InProgressCount() > maxStartingInstancesPerNode {
		return false
	}

	return true
}

// sample returns up to k items chosen uniformly from those passing ok.
func (b *BestOfK) sample(items []*nodemanager.Node, config BestOfKConfig, excludedNodes map[string]struct{}, resources nodemanager.SandboxResources, buildMachineInfo machineinfo.MachineInfo, filterByLabels bool, requiredLabels []string) []*nodemanager.Node {
	if config.K <= 0 || len(items) == 0 {
		return nil
	}

	indices := make([]int, len(items))
	for i := range indices {
		indices[i] = i
	}

	candidates := make([]*nodemanager.Node, 0, config.K)
	remaining := len(indices) // active pool is indices[:remaining]

	for len(candidates) < config.K && remaining > 0 {
		// pick from the active pool
		j := rand.Intn(remaining)
		pick := indices[j]

		// remove j from pool
		indices[j], indices[remaining-1] = indices[remaining-1], indices[j]
		remaining--

		n := items[pick]

		if b.isCandidate(n, config, excludedNodes, resources, buildMachineInfo, filterByLabels, requiredLabels) {
			candidates = append(candidates, n)
		}
	}

	return candidates
}
