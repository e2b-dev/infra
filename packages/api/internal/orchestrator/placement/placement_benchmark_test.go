package placement

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

// BenchmarkConfig contains configuration for realistic benchmark scenarios
type BenchmarkConfig struct {
	NumNodes              int
	SandboxStartRate      int           // sandboxes per second
	AvgSandboxCPU         int64         // average CPU request
	AvgSandboxMemory      int64         // average memory request in MiB
	CPUVariance           float64       // variance in CPU request (0.0 to 1.0)
	MemoryVariance        float64       // variance in memory request (0.0 to 1.0)
	ActualUsageRatio      float64       // ratio of actual usage to requested (0.0 to 1.0)
	ActualUsageVariance   float64       // variance in actual usage
	SandboxDuration       time.Duration // how long sandboxes run
	DurationVariance      float64       // variance in sandbox duration
	BenchmarkDuration     time.Duration
	NodeCPUCapacity       uint32 // CPU capacity per node
	NodeMemoryCapacity    uint64 // Memory capacity per node in bytes
	SandboxCreateDuration time.Duration
}

// LiveSandbox represents a running sandbox with its resource usage
type LiveSandbox struct {
	ID               string
	NodeID           string
	RequestedCPU     int64
	RequestedMemory  int64
	ActualCPUUsage   float64
	ActualMemUsage   float64
	StartTime        time.Time
	PlannedDuration  time.Duration
	PlacementLatency time.Duration
}

// NodeSimulator defines the common behavior of simulated nodes
type NodeSimulator interface {
	// GetNode returns the underlying nodemanager.Node object for use by the algorithm
	GetNode() *nodemanager.Node
	// PlaceSandbox attempts to place a Sandbox on the node, returns whether successful
	PlaceSandbox(sbx *LiveSandbox) bool
	// RemoveSandbox removes a Sandbox from the node
	RemoveSandbox(sandboxID string)
	// GetUtilization returns the current CPU and memory utilization (0-100)
	GetUtilization() (float64, float64)
	// GetSandboxCount returns the number of currently running Sandboxes
	GetSandboxCount() int
}

// NodeFactory defines the function signature for creating simulated nodes
type NodeFactory func(id string, config BenchmarkConfig) NodeSimulator

// StandardNode implements the original SimulatedNode logic (real-time metric synchronization)
type StandardNode struct {
	*nodemanager.Node

	mu                 sync.RWMutex
	sandboxes          map[string]*LiveSandbox
	totalPlacements    int64
	rejectedPlacements int64 // counts failed placements due to capacity
}

// Ensure StandardNode implements the interface
var _ NodeSimulator = &StandardNode{}

func NewStandardNode(id string, config BenchmarkConfig) NodeSimulator {
	return &StandardNode{
		Node: nodemanager.NewTestNode(
			id,
			api.NodeStatusReady,
			0,
			config.NodeCPUCapacity,
			nodemanager.WithSandboxSleepingClient(config.SandboxCreateDuration),
		),
		sandboxes: make(map[string]*LiveSandbox),
	}
}

func (n *StandardNode) GetNode() *nodemanager.Node {
	return n.Node
}

func (n *StandardNode) PlaceSandbox(sbx *LiveSandbox) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	metrics := n.Metrics()
	// Check capacity with overcommit (Original Logic)
	if metrics.CpuAllocated+uint32(sbx.RequestedCPU) > metrics.CpuCount*4 {
		atomic.AddInt64(&n.rejectedPlacements, 1)

		return false
	}

	// Real-time update: directly modify Node Metrics
	n.UpdateMetricsFromServiceInfoResponse(&orchestrator.ServiceInfoResponse{
		MetricSandboxesRunning:     uint32(len(n.sandboxes)) + 1,
		MetricCpuPercent:           metrics.CpuPercent + uint32(sbx.ActualCPUUsage*100),
		MetricMemoryUsedBytes:      metrics.MemoryUsedBytes + uint64(sbx.ActualMemUsage),
		MetricCpuCount:             metrics.CpuCount,
		MetricMemoryTotalBytes:     metrics.MemoryTotalBytes,
		MetricCpuAllocated:         metrics.CpuAllocated + uint32(sbx.RequestedCPU),
		MetricMemoryAllocatedBytes: metrics.MemoryAllocatedBytes + uint64(sbx.RequestedMemory)*1024*1024,
	})
	n.sandboxes[sbx.ID] = sbx
	atomic.AddInt64(&n.totalPlacements, 1)

	return true
}

func (n *StandardNode) RemoveSandbox(sandboxID string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	metrics := n.Metrics()
	if sbx, exists := n.sandboxes[sandboxID]; exists {
		n.UpdateMetricsFromServiceInfoResponse(&orchestrator.ServiceInfoResponse{
			MetricSandboxesRunning:     uint32(len(n.sandboxes)) - 1,
			MetricCpuPercent:           metrics.CpuPercent - uint32(sbx.ActualCPUUsage*100),
			MetricMemoryUsedBytes:      metrics.MemoryUsedBytes - uint64(sbx.ActualMemUsage),
			MetricCpuAllocated:         metrics.CpuAllocated - uint32(sbx.RequestedCPU),
			MetricMemoryAllocatedBytes: metrics.MemoryAllocatedBytes - uint64(sbx.RequestedMemory)*1024*1024,
			MetricCpuCount:             metrics.CpuCount,
			MetricMemoryTotalBytes:     metrics.MemoryTotalBytes,
		})
		delete(n.sandboxes, sandboxID)
	}
}

func (n *StandardNode) GetUtilization() (float64, float64) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	metrics := n.Metrics()
	var cpuUtil, memUtil float64
	if metrics.CpuCount > 0 {
		cpuUtil = ((float64(metrics.CpuPercent) / 100) / float64(metrics.CpuCount)) * 100
	}
	if metrics.MemoryTotalBytes > 0 {
		memUtil = (float64(metrics.MemoryUsedBytes) / float64(metrics.MemoryTotalBytes)) * 100
	}

	return cpuUtil, memUtil
}

func (n *StandardNode) GetSandboxCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return len(n.sandboxes)
}

// LaggyNode simulates metric lag.
// It maintains both the “real” state and “reported” state (Node Metrics).
// Real state is only synced to Node Metrics when SyncMetrics() is called.
type LaggyNode struct {
	*StandardNode // Embed StandardNode to reuse logic

	// Internal resource state (hidden from Orchestrator until SyncMetrics is called)
	realCpuAllocated     uint32
	realMemAllocated     uint64
	realSandboxesRunning uint32
}

func NewLaggyNode(id string, config BenchmarkConfig) NodeSimulator {
	base := NewStandardNode(id, config).(*StandardNode)

	return &LaggyNode{
		StandardNode: base,
	}
}

// PlaceSandbox Override: only update real state, not Node Metrics
func (n *LaggyNode) PlaceSandbox(sbx *LiveSandbox) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 1. Admission control based on real capacity (simulating node-side rejection)
	// Note: we use realCpuAllocated for the check
	metrics := n.Node.Metrics()
	if n.realCpuAllocated+uint32(sbx.RequestedCPU) > metrics.CpuCount*4 {
		atomic.AddInt64(&n.rejectedPlacements, 1)

		return false
	}

	// 2. Update real state
	n.sandboxes[sbx.ID] = sbx
	n.realSandboxesRunning++
	n.realCpuAllocated += uint32(sbx.RequestedCPU)
	n.realMemAllocated += uint64(sbx.RequestedMemory) * 1024 * 1024

	atomic.AddInt64(&n.totalPlacements, 1)

	// Key: intentionally do NOT call UpdateMetricsFromServiceInfoResponse
	// The metrics visible to Orchestrator remain unchanged until SyncMetrics is called
	return true
}

func (n *LaggyNode) RemoveSandbox(sandboxID string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if sbx, exists := n.sandboxes[sandboxID]; exists {
		n.realSandboxesRunning--
		n.realCpuAllocated -= uint32(sbx.RequestedCPU)
		n.realMemAllocated -= uint64(sbx.RequestedMemory) * 1024 * 1024
		delete(n.sandboxes, sandboxID)
	}
}

// SyncMetrics simulates heartbeat reporting, syncing real state to Orchestrator
func (n *LaggyNode) SyncMetrics() {
	n.mu.Lock()
	defer n.mu.Unlock()

	metrics := n.Node.Metrics()

	// Calculate real CPU usage (simplified as linear accumulation here, may be more complex in production)
	var totalActualCpuUsage float64
	var totalActualMemUsage float64
	for _, sbx := range n.sandboxes {
		totalActualCpuUsage += sbx.ActualCPUUsage
		totalActualMemUsage += sbx.ActualMemUsage
	}

	n.UpdateMetricsFromServiceInfoResponse(&orchestrator.ServiceInfoResponse{
		MetricSandboxesRunning:     n.realSandboxesRunning,
		MetricCpuPercent:           uint32(totalActualCpuUsage * 100),
		MetricMemoryUsedBytes:      uint64(totalActualMemUsage),
		MetricCpuAllocated:         n.realCpuAllocated,
		MetricMemoryAllocatedBytes: n.realMemAllocated,

		// Static fields remain unchanged
		MetricCpuCount:         metrics.CpuCount,
		MetricMemoryTotalBytes: metrics.MemoryTotalBytes,
	})
}

// BenchmarkMetrics contains detailed metrics from the benchmark
type BenchmarkMetrics struct {
	// Placement metrics
	TotalPlacements      int64
	SuccessfulPlacements int64
	FailedPlacements     int64
	AvgPlacementTime     time.Duration
	MaxPlacementTime     time.Duration
	MinPlacementTime     time.Duration
	P50PlacementTime     time.Duration
	P95PlacementTime     time.Duration
	P99PlacementTime     time.Duration

	// Node utilization metrics
	AvgNodeCPUUtilization float64
	MaxNodeCPUUtilization float64
	MinNodeCPUUtilization float64
	AvgNodeMemUtilization float64
	MaxNodeMemUtilization float64
	MinNodeMemUtilization float64

	// Load distribution metrics
	CPULoadStdDev            float64
	MemLoadStdDev            float64
	LoadImbalanceCoefficient float64
}

// createSimulatedNodes creates nodes using a factory function
func createSimulatedNodes(config BenchmarkConfig, factory NodeFactory) []NodeSimulator {
	if factory == nil {
		factory = NewStandardNode // Default to standard node
	}

	nodes := make([]NodeSimulator, config.NumNodes)
	for i := range config.NumNodes {
		nodes[i] = factory(fmt.Sprintf("node-%d", i), config)
	}

	return nodes
}

// Helper function: converts NodeSimulator slice to *nodemanager.Node slice (for algorithm use)
func toNodeManagerNodes(simNodes []NodeSimulator) []*nodemanager.Node {
	nodes := make([]*nodemanager.Node, len(simNodes))
	for i, n := range simNodes {
		nodes[i] = n.GetNode()
	}

	return nodes
}

// runBenchmark runs a comprehensive placement benchmark with lifecycle tracking
func runBenchmark(b *testing.B, algorithm Algorithm, config BenchmarkConfig, nodeFactory NodeFactory) *BenchmarkMetrics {
	b.Helper()

	parentCtx := b.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, config.BenchmarkDuration)
	defer cancel()

	// Create nodes using factory
	simNodes := createSimulatedNodes(config, nodeFactory)

	// Convert node list
	nodes := toNodeManagerNodes(simNodes)
	nodeMap := make(map[string]NodeSimulator)
	for _, n := range simNodes {
		nodeMap[n.GetNode().ID] = n
	}

	// Initialize metrics
	metrics := &BenchmarkMetrics{
		MinPlacementTime: time.Hour,
	}
	// Tracking structures
	var (
		mu               sync.Mutex
		placementTimes   []time.Duration
		activeSandboxes  sync.Map // sandboxID -> *LiveSandbox
		sandboxIDCounter int64

		// Metrics for time series
		recentPlacements []time.Duration
		recentSuccesses  int64
		recentFailures   int64
	)

	// Start sandbox cleanup goroutine
	stopCleanup := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				now := time.Now()
				// Check and remove expired sandboxes
				activeSandboxes.Range(func(key, value any) bool {
					sandbox := value.(*LiveSandbox)
					if now.Sub(sandbox.StartTime) > sandbox.PlannedDuration {
						// Remove from node
						if node, exists := nodeMap[sandbox.NodeID]; exists {
							node.RemoveSandbox(sandbox.ID)
						}
						// Remove from active list
						activeSandboxes.Delete(key)
					}

					return true
				})
			case <-stopCleanup:
				return
			}
		}
	}()

	// Sandbox generation goroutine
	var wg sync.WaitGroup
	stopGeneration := make(chan struct{})

	wg.Go(func() {
		ticker := time.NewTicker(time.Second / time.Duration(config.SandboxStartRate))
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Generate sandbox with variance
				sandboxID := atomic.AddInt64(&sandboxIDCounter, 1)

				cpuVariance := (rand.Float64()*2 - 1) * config.CPUVariance
				requestedCPU := max(int64(float64(config.AvgSandboxCPU)*(1+cpuVariance)), 1)

				memVariance := (rand.Float64()*2 - 1) * config.MemoryVariance
				requestedMem := max(int64(float64(config.AvgSandboxMemory)*(1+memVariance)), 1)

				// Calculate actual usage with variance
				actualUsageRatio := config.ActualUsageRatio + (rand.Float64()*2-1)*config.ActualUsageVariance
				if actualUsageRatio < 0.1 {
					actualUsageRatio = 0.1
				}
				if actualUsageRatio > 1.0 {
					actualUsageRatio = 1.0
				}

				// Calculate duration with variance
				durationVariance := (rand.Float64()*2 - 1) * config.DurationVariance
				duration := time.Duration(float64(config.SandboxDuration) * (1 + durationVariance))

				sandbox := &LiveSandbox{
					ID:              fmt.Sprintf("sandbox-%d", sandboxID),
					RequestedCPU:    requestedCPU,
					RequestedMemory: requestedMem,
					ActualCPUUsage:  float64(requestedCPU) * actualUsageRatio,
					ActualMemUsage:  float64(requestedMem) * actualUsageRatio * 1024 * 1024, // Convert MiB to bytes
					StartTime:       time.Now(),
					PlannedDuration: duration,
				}

				// Try to place the sandbox
				wg.Go(func(sbx *LiveSandbox) func() {
					return func() {
						placementStart := time.Now()
						node, err := PlaceSandbox(ctx, algorithm, nodes, nil, &orchestratorgrpc.SandboxCreateRequest{Sandbox: &orchestratorgrpc.SandboxConfig{
							SandboxId: sbx.ID,
							Vcpu:      sbx.RequestedCPU,
							RamMb:     sbx.RequestedMemory,
						}}, machineinfo.MachineInfo{}, false, nil)

						placementTime := time.Since(placementStart)
						sbx.PlacementLatency = placementTime

						mu.Lock()
						placementTimes = append(placementTimes, placementTime)
						recentPlacements = append(recentPlacements, placementTime)
						metrics.TotalPlacements++

						if placementTime < metrics.MinPlacementTime {
							metrics.MinPlacementTime = placementTime
						}
						if placementTime > metrics.MaxPlacementTime {
							metrics.MaxPlacementTime = placementTime
						}

						success := false
						if err == nil && node != nil {
							// Find the simulated node and place the sandbox
							if simNode, exists := nodeMap[node.ID]; exists {
								sbx.NodeID = node.ID
								if simNode.PlaceSandbox(sbx) {
									activeSandboxes.Store(sbx.ID, sbx)
									metrics.SuccessfulPlacements++
									atomic.AddInt64(&recentSuccesses, 1)
									success = true
								}
							}
						}

						if !success {
							metrics.FailedPlacements++
							atomic.AddInt64(&recentFailures, 1)
						}
						mu.Unlock()
					}
				}(sandbox))

			case <-stopGeneration:
				return
			case <-ctx.Done():
				return
			}
		}
	})

	// Wait for benchmark duration
	<-ctx.Done()
	close(stopGeneration)
	wg.Wait()
	close(stopCleanup)

	// Calculate final metrics
	calculateFinalMetrics(metrics, simNodes, placementTimes)

	return metrics
}

// calculateFinalMetrics calculates comprehensive final metrics
func calculateFinalMetrics(metrics *BenchmarkMetrics, nodes []NodeSimulator, placementTimes []time.Duration) {
	// Calculate placement time metrics
	if len(placementTimes) > 0 {
		var totalTime time.Duration
		for _, t := range placementTimes {
			totalTime += t
		}
		metrics.AvgPlacementTime = totalTime / time.Duration(len(placementTimes))

		// Sort for percentiles
		slices.Sort(placementTimes)

		metrics.P50PlacementTime = placementTimes[len(placementTimes)*50/100]
		metrics.P95PlacementTime = placementTimes[len(placementTimes)*95/100]
		if len(placementTimes) > 0 {
			metrics.P99PlacementTime = placementTimes[len(placementTimes)*99/100]
		}
	}

	// Calculate node utilization metrics
	var cpuUtils, memUtils []float64
	metrics.MinNodeCPUUtilization = 100.0
	metrics.MinNodeMemUtilization = 100.0

	for _, node := range nodes {
		cpuUtil, memUtil := node.GetUtilization()
		cpuUtils = append(cpuUtils, cpuUtil)
		memUtils = append(memUtils, memUtil)

		// Track min/max
		if cpuUtil > metrics.MaxNodeCPUUtilization {
			metrics.MaxNodeCPUUtilization = cpuUtil
		}
		if cpuUtil < metrics.MinNodeCPUUtilization {
			metrics.MinNodeCPUUtilization = cpuUtil
		}
		if memUtil > metrics.MaxNodeMemUtilization {
			metrics.MaxNodeMemUtilization = memUtil
		}
		if memUtil < metrics.MinNodeMemUtilization {
			metrics.MinNodeMemUtilization = memUtil
		}
	}

	// Calculate averages and standard deviations
	if len(nodes) > 0 {
		var totalCPU, totalMem float64
		for i := range cpuUtils {
			totalCPU += cpuUtils[i]
			totalMem += memUtils[i]
		}
		metrics.AvgNodeCPUUtilization = totalCPU / float64(len(nodes))
		metrics.AvgNodeMemUtilization = totalMem / float64(len(nodes))

		// Calculate standard deviations
		var cpuSumSquares, memSumSquares float64
		for i := range cpuUtils {
			cpuDiff := cpuUtils[i] - metrics.AvgNodeCPUUtilization
			memDiff := memUtils[i] - metrics.AvgNodeMemUtilization
			cpuSumSquares += cpuDiff * cpuDiff
			memSumSquares += memDiff * memDiff
		}
		metrics.CPULoadStdDev = math.Sqrt(cpuSumSquares / float64(len(nodes)))
		metrics.MemLoadStdDev = math.Sqrt(memSumSquares / float64(len(nodes)))

		// Calculate coefficient of variation (load imbalance)
		if metrics.AvgNodeCPUUtilization > 0 {
			metrics.LoadImbalanceCoefficient = metrics.CPULoadStdDev / metrics.AvgNodeCPUUtilization
		}
	}
}

// BenchmarkPlacementComparison runs comprehensive comparison with lifecycle tracking
func BenchmarkPlacementComparison(t *testing.B) {
	config := BenchmarkConfig{
		NumNodes:              50,
		SandboxStartRate:      30,
		AvgSandboxCPU:         4,
		AvgSandboxMemory:      1024,
		CPUVariance:           0.8,
		MemoryVariance:        0.4,
		ActualUsageRatio:      0.4,
		ActualUsageVariance:   1,
		SandboxDuration:       5 * time.Second,
		DurationVariance:      5,
		BenchmarkDuration:     time.Minute,
		NodeCPUCapacity:       32,
		SandboxCreateDuration: time.Millisecond * 0,
	}

	algorithms := []struct {
		name string
		algo Algorithm
	}{
		{"BestOfK_K3", NewBestOfK(DefaultBestOfKConfig())},
		{"BestOfK_K5", NewBestOfK(BestOfKConfig{R: 4, K: 5, Alpha: 0.5})},
	}

	for _, alg := range algorithms {
		t.Run(alg.name, func(t *testing.B) {
			metrics := runBenchmark(t, alg.algo, config, NewStandardNode)

			t.Logf("\n=== %s Results ===", alg.name)
			t.Logf("Placement Performance:")
			t.Logf("  Total: %d, Success: %d (%.1f%%), Failed: %d",
				metrics.TotalPlacements,
				metrics.SuccessfulPlacements,
				float64(metrics.SuccessfulPlacements)/float64(metrics.TotalPlacements)*100,
				metrics.FailedPlacements)
			t.Logf("  Latency - Avg: %v, P50: %v, P95: %v, P99: %v",
				metrics.AvgPlacementTime,
				metrics.P50PlacementTime,
				metrics.P95PlacementTime,
				metrics.P99PlacementTime)

			t.Logf("\nNode Utilization:")
			t.Logf("  CPU - Avg: %.1f%%, Min: %.1f%%, Max: %.1f%%, StdDev: %.1f%%",
				metrics.AvgNodeCPUUtilization,
				metrics.MinNodeCPUUtilization,
				metrics.MaxNodeCPUUtilization,
				metrics.CPULoadStdDev)
			t.Logf("  Memory - Avg: %.1f%%, Min: %.1f%%, Max: %.1f%%, StdDev: %.1f%%",
				metrics.AvgNodeMemUtilization,
				metrics.MinNodeMemUtilization,
				metrics.MaxNodeMemUtilization,
				metrics.MemLoadStdDev)
			t.Logf("  Load Imbalance Coefficient: %.3f", metrics.LoadImbalanceCoefficient)
		})
	}
}

// BenchmarkPlacementDistribution visualizes load distribution across nodes (simulating metric lag)
// Run command: go test -v -bench=BenchmarkPlacementDistribution -run=^$ ./internal/orchestrator/placement
func BenchmarkPlacementDistribution(b *testing.B) {
	// Scenario config: “thundering herd” test under extremely high concurrency
	// Use LaggyNode to simulate metric reporting delay: Orchestrator always sees stale metrics
	config := BenchmarkConfig{
		NumNodes:              10,   // Fewer nodes to make load hotspots more apparent
		SandboxStartRate:      20,   // 20 requests per second (burst traffic)
		AvgSandboxCPU:         1,    // 1 vCPU per Sandbox
		AvgSandboxMemory:      1024, // 1024 MiB
		CPUVariance:           0.0,  // Fixed spec for easier distribution observation
		MemoryVariance:        0.0,
		ActualUsageRatio:      1.0, // Assume full utilization
		ActualUsageVariance:   0.0,
		SandboxDuration:       1 * time.Minute,          // Long enough to ensure no release during test
		BenchmarkDuration:     25 * time.Second,         // Run long enough to trigger at least one heartbeat sync (20s ticker)
		NodeCPUCapacity:       192,                      // 192 vCPU per Node
		NodeMemoryCapacity:    1024 * 1024 * 1024 * 512, // 512 GiB per Node
		SandboxCreateDuration: time.Millisecond,
	}

	algorithms := []struct {
		name string
		algo Algorithm
	}{
		// Compare algorithms here. Expect LeastBusy to have serious hotspot issues.
		// {"LeastBusy", &LeastBusyAlgorithm{}},
		{"BestOfK_K3", NewBestOfK(DefaultBestOfKConfig())},
		{"BestOfK_K5", NewBestOfK(BestOfKConfig{R: 4, K: 5, Alpha: 0.5})},
	}

	for _, alg := range algorithms {
		b.Run(alg.name, func(b *testing.B) {
			b.Logf("Running distribution test for %s with LaggyNodes...", alg.name)

			parentCtx := b.Context()
			if parentCtx == nil {
				parentCtx = context.Background()
			}
			ctx, cancel := context.WithTimeout(parentCtx, config.BenchmarkDuration)
			defer cancel()

			// 1. Create nodes using NewLaggyNode (default: only update internal real state, not Metrics)
			simNodes := createSimulatedNodes(config, NewLaggyNode)
			nodes := toNodeManagerNodes(simNodes)
			nodeMap := make(map[string]NodeSimulator)

			// 2. Warmup: apply slight random baseline noise to nodes
			// This breaks the initial tie in LeastBusy state, causing it to quickly lock onto a “victim”
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			for _, n := range simNodes {
				nodeMap[n.GetNode().ID] = n
				if ln, ok := n.(*LaggyNode); ok {
					ln.realCpuAllocated = uint32(rng.Intn(5)) // 0-4 CPU random baseline noise
					ln.SyncMetrics()                          // Initial sync once
				}
			}

			// 3. Start heartbeat simulator
			// Simulates reporting metrics every 20s in a real environment.
			// All requests between heartbeats see stale metrics.
			go func() {
				ticker := time.NewTicker(20 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						for _, n := range simNodes {
							if ln, ok := n.(*LaggyNode); ok {
								ln.SyncMetrics()
							}
						}
					}
				}
			}()

			// 4. Concurrently generate Sandbox requests
			var wg sync.WaitGroup
			wg.Go(func() {
				ticker := time.NewTicker(time.Second / time.Duration(config.SandboxStartRate))
				defer ticker.Stop()

				var sandboxIDCounter int64
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						sandboxID := atomic.AddInt64(&sandboxIDCounter, 1)
						sbx := &LiveSandbox{
							ID:              fmt.Sprintf("sbx-%d", sandboxID),
							RequestedCPU:    config.AvgSandboxCPU,
							RequestedMemory: config.AvgSandboxMemory,
							ActualCPUUsage:  float64(config.AvgSandboxCPU),
							ActualMemUsage:  float64(config.AvgSandboxMemory) * 1024 * 1024,
							StartTime:       time.Now(),
						}

						wg.Add(1)
						go func(s *LiveSandbox) {
							// Execute placement algorithm
							node, err := PlaceSandbox(ctx, alg.algo, nodes, nil, &orchestratorgrpc.SandboxCreateRequest{
								Sandbox: &orchestratorgrpc.SandboxConfig{
									SandboxId: s.ID,
									Vcpu:      s.RequestedCPU,
									RamMb:     s.RequestedMemory,
								},
							}, machineinfo.MachineInfo{}, false, nil)

							if err == nil && node != nil {
								if simNode, ok := nodeMap[node.ID]; ok {
									// Placement successful, but Metrics won't update immediately (LaggyNode feature)
									simNode.PlaceSandbox(s)
								}
							}
						}(sbx)
					}
				}
			})

			<-ctx.Done()
			wg.Wait()

			// 5. Result visualization
			b.Logf("\n=== Distribution Results (Laggy Metrics): %s ===", alg.name)

			maxCount := 0
			totalCount := 0
			for _, n := range simNodes {
				count := n.GetSandboxCount()
				totalCount += count
				if count > maxCount {
					maxCount = count
				}
			}

			if totalCount == 0 {
				b.Log("No sandboxes placed.")

				return
			}

			// Print ASCII histogram
			for i, n := range simNodes {
				count := n.GetSandboxCount()
				barLen := 0
				if maxCount > 0 {
					barLen = int(float64(count) / float64(maxCount) * 40) // Max 40 characters
				}

				var bar strings.Builder
				for range barLen {
					bar.WriteString("█")
				}

				// Force Sync before printing to ensure we see the final real values
				if ln, ok := n.(*LaggyNode); ok {
					ln.SyncMetrics()
				}
				cpuUtil, _ := n.GetUtilization()

				b.Logf("Node %02d |%s| %d (CPU: %.1f%%)", i, fmt.Sprintf("%-40s", bar.String()), count, cpuUtil)
			}

			// Calculate distribution statistics (CV coefficient of variation)
			avg := float64(totalCount) / float64(len(simNodes))
			var sumDiffSq float64
			for _, n := range simNodes {
				diff := float64(n.GetSandboxCount()) - avg
				sumDiffSq += diff * diff
			}
			stdDev := math.Sqrt(sumDiffSq / float64(len(simNodes)))
			cv := 0.0
			if avg > 0 {
				cv = stdDev / avg
			}
			b.Logf("Stats: Total=%d, Avg=%.1f, StdDev=%.2f, Imbalance(CV)=%.3f", totalCount, avg, stdDev, cv)
		})
	}
}
