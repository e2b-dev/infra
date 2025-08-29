package placement

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
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

// SimulatedNode represents a node with realistic resource tracking
type SimulatedNode struct {
	*nodemanager.Node
	mu                 sync.RWMutex
	sandboxes          map[string]*LiveSandbox
	totalPlacements    int64
	rejectedPlacements int64
	lastUpdateTime     time.Time
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

// createSimulatedNodes creates nodes with realistic resource tracking
func createSimulatedNodes(config BenchmarkConfig) []*SimulatedNode {
	nodes := make([]*SimulatedNode, config.NumNodes)
	for i := 0; i < config.NumNodes; i++ {
		// Create base node
		baseNode := nodemanager.NewTestNode(
			fmt.Sprintf("node-%d", i),
			api.NodeStatusReady,
			0, // Start with no load
			config.NodeCPUCapacity,
			nodemanager.WithSandboxSleepingClient(config.SandboxCreateDuration),
		)

		simNode := &SimulatedNode{
			Node:           baseNode,
			sandboxes:      make(map[string]*LiveSandbox),
			lastUpdateTime: time.Now(),
		}
		nodes[i] = simNode
	}
	return nodes
}

// placeSandbox places a sandbox on the node
func (n *SimulatedNode) placeSandbox(sandbox *LiveSandbox) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	metrics := n.Metrics()
	// Check capacity with overcommit
	if metrics.CpuAllocated+uint32(sandbox.RequestedCPU) > metrics.CpuCount*4 { // 4x overcommit
		atomic.AddInt64(&n.rejectedPlacements, 1)
		return false
	}

	n.AddSandbox(&instance.InstanceInfo{
		VCpu:  sandbox.RequestedCPU,
		RamMB: sandbox.RequestedMemory,
	})

	n.UpdateMetricsFromServiceInfoResponse(&orchestrator.ServiceInfoResponse{
		MetricSandboxesRunning: uint32(len(n.sandboxes)) + 1,
		// Host system usage metrics
		MetricCpuPercent:      metrics.CpuPercent + uint32(sandbox.ActualCPUUsage*100),
		MetricMemoryUsedBytes: metrics.MemoryUsedBytes + uint64(sandbox.ActualMemUsage),
		// Host system total resources
		MetricCpuCount:         metrics.CpuCount,
		MetricMemoryTotalBytes: metrics.MemoryTotalBytes,
		// Allocated resources to sandboxes
		MetricCpuAllocated:         metrics.CpuAllocated + uint32(sandbox.RequestedCPU),
		MetricMemoryAllocatedBytes: metrics.MemoryAllocatedBytes + uint64(sandbox.RequestedMemory)*1024*1024,
	})
	n.sandboxes[sandbox.ID] = sandbox
	atomic.AddInt64(&n.totalPlacements, 1)

	return true
}

// removeSandbox removes a sandbox from the node
func (n *SimulatedNode) removeSandbox(sandboxID string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	metrics := n.Metrics()

	if sandbox, exists := n.sandboxes[sandboxID]; exists {

		n.RemoveSandbox(&instance.InstanceInfo{
			VCpu:  sandbox.RequestedCPU,
			RamMB: sandbox.RequestedMemory,
		})
		n.UpdateMetricsFromServiceInfoResponse(&orchestrator.ServiceInfoResponse{
			MetricSandboxesRunning: uint32(len(n.sandboxes)) - 1,

			MetricCpuPercent:      metrics.CpuPercent - uint32(sandbox.ActualCPUUsage*100),
			MetricMemoryUsedBytes: metrics.MemoryUsedBytes - uint64(sandbox.ActualMemUsage),

			MetricCpuAllocated:         metrics.CpuAllocated - uint32(sandbox.RequestedCPU),
			MetricMemoryAllocatedBytes: metrics.MemoryAllocatedBytes - uint64(sandbox.RequestedMemory)*1024*1024,

			MetricCpuCount:         metrics.CpuCount,
			MetricMemoryTotalBytes: metrics.MemoryTotalBytes,
		})

		delete(n.sandboxes, sandboxID)
	}
}

// getUtilization returns current CPU and memory utilization percentages
func (n *SimulatedNode) getUtilization() (cpuUtil, memUtil float64) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	metrics := n.Metrics()
	if metrics.CpuCount > 0 {
		cpuUtil = ((float64(metrics.CpuPercent) / 100) / float64(metrics.CpuCount)) * 100
	}
	if metrics.MemoryTotalBytes > 0 {
		memUtil = (float64(metrics.MemoryUsedBytes) / float64(metrics.MemoryTotalBytes)) * 100
	}

	return cpuUtil, memUtil
}

// runBenchmark runs a comprehensive placement benchmark with lifecycle tracking
func runBenchmark(_ *testing.B, algorithm Algorithm, config BenchmarkConfig) *BenchmarkMetrics {
	ctx, cancel := context.WithTimeout(context.Background(), config.BenchmarkDuration)
	defer cancel()

	// Create simulated nodes
	simNodes := createSimulatedNodes(config)

	// Convert to nodemanager.Node slice for algorithm
	nodes := make([]*nodemanager.Node, len(simNodes))
	nodeMap := make(map[string]*SimulatedNode)
	for i, n := range simNodes {
		nodes[i] = n.Node
		nodeMap[n.ID] = n
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
				activeSandboxes.Range(func(key, value interface{}) bool {
					sandbox := value.(*LiveSandbox)
					if now.Sub(sandbox.StartTime) > sandbox.PlannedDuration {
						// Remove from node
						if node, exists := nodeMap[sandbox.NodeID]; exists {
							node.removeSandbox(sandbox.ID)
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second / time.Duration(config.SandboxStartRate))
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Generate sandbox with variance
				sandboxID := atomic.AddInt64(&sandboxIDCounter, 1)

				cpuVariance := (rand.Float64()*2 - 1) * config.CPUVariance
				requestedCPU := int64(float64(config.AvgSandboxCPU) * (1 + cpuVariance))
				if requestedCPU < 1 {
					requestedCPU = 1
				}

				memVariance := (rand.Float64()*2 - 1) * config.MemoryVariance
				requestedMem := int64(float64(config.AvgSandboxMemory) * (1 + memVariance))
				if requestedMem < 1 {
					requestedMem = 1
				}

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
				wg.Add(1)
				go func(sbx *LiveSandbox) {
					defer wg.Done()

					placementStart := time.Now()
					node, err := PlaceSandbox(ctx, noop.Tracer{}, algorithm, nodes, nil, &orchestratorgrpc.SandboxCreateRequest{Sandbox: &orchestratorgrpc.SandboxConfig{
						Vcpu:  sbx.RequestedCPU,
						RamMb: sbx.RequestedMemory,
					}})

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
							if simNode.placeSandbox(sbx) {
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
				}(sandbox)

			case <-stopGeneration:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

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
func calculateFinalMetrics(metrics *BenchmarkMetrics, nodes []*SimulatedNode, placementTimes []time.Duration) {
	// Calculate placement time metrics
	if len(placementTimes) > 0 {
		var totalTime time.Duration
		for _, t := range placementTimes {
			totalTime += t
		}
		metrics.AvgPlacementTime = totalTime / time.Duration(len(placementTimes))

		// Sort for percentiles
		sort.Slice(placementTimes, func(i, j int) bool {
			return placementTimes[i] < placementTimes[j]
		})

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
		cpuUtil, memUtil := node.getUtilization()
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
		{"LeastBusy", &LeastBusyAlgorithm{}},
		{"BestOfK_K3", NewBestOfK(DefaultBestOfKConfig())},
		{"BestOfK_K5", NewBestOfK(BestOfKConfig{R: 4, K: 5, Alpha: 0.5})},
	}

	for _, alg := range algorithms {
		t.Run(alg.name, func(t *testing.B) {
			metrics := runBenchmark(&testing.B{}, alg.algo, config)

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
