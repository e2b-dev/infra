package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type benchmarkConfig struct {
	sandboxesPerNodeCount int
	nodeCount             int
	duration              time.Duration
	emitInterval          time.Duration
}

type node struct {
	sbxCount        int
	sandboxObserver *telemetry.SandboxObserver
}

func runBenchmark(ctx context.Context, cfg benchmarkConfig) error {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, cfg.duration)
	defer cancel()

	// Create a wait group to track all goroutines
	var wg sync.WaitGroup

	// Channel to collect errors from goroutines
	errChan := make(chan error, cfg.nodeCount)

	// Start time for the benchmark
	startTime := time.Now()

	// Launch goroutines for each node
	for i := 0; i < cfg.nodeCount; i++ {
		sandboxObserver, err := telemetry.NewSandboxObserver(ctx, "benchmark-metrics", strconv.Itoa(i), cfg.emitInterval)
		if err != nil {
			return fmt.Errorf("failed to create metrics provider for node %d: %w", i, err)
		}

		n := &node{
			// Put some randomness in the client creation to simulate different nodes
			sbxCount:        int((0.5 + rand.Float64()) * float64(cfg.sandboxesPerNodeCount)),
			sandboxObserver: sandboxObserver,
		}

		wg.Add(1)
		go func(nodeID int) {
			defer wg.Done()
			zap.L().Info("Starting node", zap.Int("nodeID", nodeID), zap.Int("sandboxesPerNodeCount", n.sbxCount))

			// Jitter
			time.Sleep(time.Duration(rand.Intn(5)) * time.Millisecond)
			unregistres := make([]func() error, 0, n.sbxCount)
			for j := 0; j < n.sbxCount; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					sandboxID := fmt.Sprintf("sandbox-%d-%d", nodeID, j)
					teamID := fmt.Sprintf("team-%d", nodeID)
					unregister, err := n.sandboxObserver.StartObserving(sandboxID, teamID, func(ctx context.Context) (*telemetry.SandboxMetrics, error) {
						// Simulate getting metrics from a sandbox
						// In a real scenario, this would fetch actual metrics from the sandbox
						return &telemetry.SandboxMetrics{
							Timestamp:      time.Now().Unix(),
							CPUCount:       rand.Int63n(16) + 1,       // Random CPU count between 1 and 16
							CPUUsedPercent: rand.Float64() * 100,      // Random CPU usage percentage
							MemTotalMiB:    rand.Int63n(16000) + 1024, // Random memory total between 1GB and 16GB
							MemUsedMiB:     rand.Int63n(16000) + 1024, // Random memory used between 1GB and 16GB
						}, nil
					})
					if err != nil {
						errChan <- fmt.Errorf("failed to start monitoring for sandbox %s: %w", sandboxID, err)
						return
					}

					unregistres = append(unregistres, unregister.Unregister)
				}
			}

			// Wait for the duration of the benchmark
			select {
			case <-ctx.Done():
				zap.L().Info("Benchmark duration reached, stopping node", zap.Int("nodeID", nodeID))
			}

			// Unregister all sandboxes
			for _, unregister := range unregistres {
				if err := unregister(); err != nil {
					errChan <- fmt.Errorf("failed to unregister sandbox for node %d: %w", nodeID, err)
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Calculate and print benchmark results
	duration := time.Since(startTime)
	totalMetrics := cfg.nodeCount * cfg.sandboxesPerNodeCount * int(cfg.duration.Seconds()/cfg.emitInterval.Seconds())
	metricsPerSecond := float64(totalMetrics) / duration.Seconds()

	fmt.Printf("\nBenchmark Results:\n")
	fmt.Printf("Total Duration: %v\n", duration)
	fmt.Printf("Total Metrics: %d\n", totalMetrics)
	fmt.Printf("Metrics emit interval: %.2fs\n", cfg.emitInterval.Seconds())
	fmt.Printf("Metrics per second: %.2f\n", metricsPerSecond)
	fmt.Printf("Nodes: %d\n", cfg.nodeCount)
	fmt.Printf("Metrics per node: %d\n", totalMetrics/cfg.sandboxesPerNodeCount)

	return nil
}

func main() {
	// Parse command line flags
	cfg := benchmarkConfig{}
	flag.IntVar(&cfg.nodeCount, "nodes", 50, "Number of concurrent nodes")
	flag.IntVar(&cfg.sandboxesPerNodeCount, "sandboxes", 200, "Number of concurrent sandboxes per node")
	flag.DurationVar(&cfg.emitInterval, "emit-interval", 5*time.Second, "Metrics emit interval")
	flag.DurationVar(&cfg.duration, "duration", 10*time.Minute, "Benchmark duration")
	flag.Parse()

	// Initialize logger
	logger, _ := zap.NewDevelopment()
	zap.ReplaceGlobals(logger)

	ctx := context.Background()

	// Run the benchmark
	if err := runBenchmark(ctx, cfg); err != nil {
		zap.L().Fatal("benchmark failed", zap.Error(err))
	}
}
