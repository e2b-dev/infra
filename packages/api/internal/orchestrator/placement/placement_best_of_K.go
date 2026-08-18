package placement

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
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
func (b *BestOfK) Score(node *nodemanager.Node, resources nodemanager.SandboxResources, config BestOfKConfig) float64 {
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

	return (cpuRequested + float64(reserved) + config.Alpha*usageAvg) / totalCapacity
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

// nodeRejectionCounts tracks how many nodes were filtered out for each reason
// during a single placement attempt.
type nodeRejectionCounts struct {
	notAccepting  int
	cpuMismatch   int
	labelMismatch int
	excluded      int
}

// chooseNode selects the best node for placing a VM with the given quota
func (b *BestOfK) chooseNode(_ context.Context, nodes []*nodemanager.Node, excludedNodes map[string]struct{}, resources nodemanager.SandboxResources, buildMachineInfo machineinfo.MachineInfo, filterByLabels bool, requiredLabels []string) (bestNode *nodemanager.Node, err error) {
	// Fix the config, we want to dynamically update it
	config := b.getConfig()

	// Filter eligible nodes
	candidates, rejections := b.sample(nodes, config, excludedNodes, buildMachineInfo, filterByLabels, requiredLabels)

	// Find the best node among candidates
	bestScore := math.MaxFloat64

	for _, node := range candidates {
		// Calculate score
		score := b.Score(node, resources, config)

		if score < bestScore {
			bestNode = node
			bestScore = score
		}
	}

	if bestNode == nil {
		return nil, FailedToPlaceSandboxError{
			filterByLabels:   filterByLabels,
			requiredLabels:   requiredLabels,
			buildMachineInfo: buildMachineInfo,
			totalNodes:       len(nodes),
			rejections:       rejections,
		}
	}

	return bestNode, nil
}

type FailedToPlaceSandboxError struct {
	filterByLabels   bool
	requiredLabels   []string
	buildMachineInfo machineinfo.MachineInfo
	totalNodes       int
	rejections       nodeRejectionCounts
}

var _ error = FailedToPlaceSandboxError{}

func (e FailedToPlaceSandboxError) Error() string {
	r := e.rejections
	msg := fmt.Sprintf(
		"no compatible node found (%d nodes checked: %d not-accepting, %d cpu-incompatible, %d label-filtered, %d excluded)",
		e.totalNodes, r.notAccepting, r.cpuMismatch, r.labelMismatch, r.excluded,
	)

	if e.buildMachineInfo.CPUArchitecture != "" {
		msg += fmt.Sprintf(
			"; build cpu: arch=%s family=%s model=%s",
			e.buildMachineInfo.CPUArchitecture,
			e.buildMachineInfo.CPUFamily,
			e.buildMachineInfo.CPUModel,
		)
	}

	if e.filterByLabels && len(e.requiredLabels) > 0 {
		msg += fmt.Sprintf("; required labels: %v", e.requiredLabels)
	}

	return msg
}

// sample returns up to k items chosen uniformly from those passing ok.
func (b *BestOfK) sample(items []*nodemanager.Node, config BestOfKConfig, excludedNodes map[string]struct{}, buildMachineInfo machineinfo.MachineInfo, filterByLabels bool, requiredLabels []string) ([]*nodemanager.Node, nodeRejectionCounts) {
	var rejections nodeRejectionCounts

	if config.K <= 0 || len(items) == 0 {
		return nil, rejections
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

		// Excluded filter
		if _, ok := excludedNodes[n.ID]; ok {
			rejections.excluded++
			continue
		}

		// If the node can't take new sandboxes, skip it
		if !n.CanAcceptNewRequests() {
			rejections.notAccepting++
			continue
		}

		// Skip if node is not CPU compatible
		if !isNodeCPUCompatible(n, buildMachineInfo) {
			rejections.cpuMismatch++
			continue
		}

		// Skip if node doesn't have the required labels
		if filterByLabels && !isNodeLabelsCompatible(n, requiredLabels) {
			rejections.labelMismatch++
			continue
		}

		candidates = append(candidates, n)
	}

	return candidates, rejections
}
