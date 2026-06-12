//go:build linux

// Real Firecracker VM concurrent benchmark.
//
// Measures sandbox creation latency using actual Firecracker VMs (not dummy orchestrator).
// Uses locally cached template and kernel.
//
// Run with:
//
//	sudo modprobe nbd
//	echo 1024 | sudo tee /proc/sys/vm/nr_hugepages
//	sudo $(which go) test -run='^$' -bench=BenchmarkRealFirecracker -benchtime=10x -timeout=30m -v \
//	  CONCURRENCY_LEVELS=1,2,5,10
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/cgroup"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// BenchmarkRealFirecracker benchmarks concurrent sandbox creation using
// real Firecracker VMs. It uses locally cached templates.
func BenchmarkRealFirecracker(b *testing.B) {
	if os.Geteuid() != 0 {
		b.Skip("skipping benchmark because not running as root")
	}

	levels := parseRealConcurrencyLevels()

	// Configuration matching our cached template
	const (
		kernelVersion = "vmlinux-6.1.102"
		fcVersion     = "v1.12.1_210cbac"
		templateID    = "fcb33d09-3141-42c4-8d3b-c2df411681db"
		buildID       = "ba6aae36-74f7-487a-b6f7-74fd7c94e479"
		useHugePages  = false // cached template was built without hugepages
	)

	// cache paths
	persistenceDir := getPersistenceDir()
	kernelsDir := filepath.Join(persistenceDir, "kernels")
	require.NoError(b, os.MkdirAll(kernelsDir, 0o755))

	tempDir := b.TempDir()

	abs := func(s string) string {
		return utils.Must(filepath.Abs(s))
	}

	// kernel download (will use cache if available)
	linuxKernelURL, err := url.JoinPath("https://storage.googleapis.com/e2b-prod-public-builds/kernels/", kernelVersion, "vmlinux.bin")
	require.NoError(b, err)
	linuxKernelFilename := filepath.Join(kernelsDir, kernelVersion, "vmlinux.bin")
	downloadKernel(b, linuxKernelFilename, linuxKernelURL)

	// env vars - point to local directories
	b.Setenv("ARTIFACTS_REGISTRY_PROVIDER", "Local")
	b.Setenv("FIRECRACKER_VERSIONS_DIR", abs(filepath.Join("..", "..", "fc-versions", "builds")))
	b.Setenv("HOST_ENVD_PATH", abs(filepath.Join("..", "..", "envd", "bin", "envd")))
	b.Setenv("HOST_KERNELS_DIR", abs(kernelsDir))
	b.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", abs(filepath.Join(persistenceDir, "templates")))
	b.Setenv("ORCHESTRATOR_BASE_PATH", tempDir)
	b.Setenv("SNAPSHOT_CACHE_DIR", abs(filepath.Join(tempDir, "snapshot-cache")))
	b.Setenv("STORAGE_PROVIDER", "Local")
	b.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")

	config, err := cfg.Parse()
	require.NoError(b, err)

	for _, subdir := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
		require.NoError(b, os.MkdirAll(filepath.Join(tempDir, subdir), 0o755))
	}

	l, err := logger.NewDevelopmentLogger()
	require.NoError(b, err)
	sbxlogger.SetSandboxLoggerInternal(l)

	slotStorage, err := network.NewStorageLocal(b.Context(), config.NetworkConfig, network.NoopEgressProxy{})
	require.NoError(b, err)
	networkPool := network.NewPool(network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, slotStorage, config.NetworkConfig)
	go func() {
		networkPool.Populate(b.Context())
		l.Info(b.Context(), "network pool populated")
	}()
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		assert.NoError(b, networkPool.Close(ctx))
	})

	devicePool, err := nbd.NewDevicePool(config.NBDPoolSize)
	require.NoError(b, err, "do you have the nbd kernel module installed?")
	go func() {
		devicePool.Populate(b.Context())
		l.Info(b.Context(), "device pool populated")
	}()
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		assert.NoError(b, devicePool.Close(ctx))
	})

	featureFlags, err := featureflags.NewClient()
	require.NoError(b, err)
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		assert.NoError(b, featureFlags.Close(ctx))
	})

	limiter, err := limit.New(b.Context(), featureFlags)
	require.NoError(b, err)

	persistence, err := storage.GetStorageProvider(b.Context(), storage.TemplateStorageConfig.WithLimiter(limiter))
	require.NoError(b, err)

	blockMetrics, err := blockmetrics.NewMetrics(&noop.MeterProvider{})
	require.NoError(b, err)

	templateCache, err := template.NewCache(config, featureFlags, persistence, blockMetrics, peerclient.NopResolver())
	require.NoError(b, err)
	templateCache.Start(b.Context())
	b.Cleanup(templateCache.Stop)

	cgroupManager, err := cgroup.NewManager()
	require.NoError(b, err, "cgroups v2 not available - running as root?")
	require.NoError(b, cgroupManager.Initialize(b.Context()), "failed to initialize root cgroup")

	sandboxes := sandbox.NewSandboxesMap()
	sandboxFactory := sandbox.NewFactory(
		config.BuilderConfig, networkPool, devicePool,
		featureFlags, hoststats.NewNoopDelivery(), cgroupManager, network.NewNoopEgressProxy(), sandboxes,
		memory.NewSharedMemfileManager(),
	)

	accessToken := "access-token"
	sandboxConfig := sandbox.NewConfig(sandbox.Config{
		BaseTemplateID:  templateID,
		Vcpu:            2,
		RamMB:           512,
		TotalDiskSizeMB: 2 * 1024,
		HugePages:       useHugePages,
		Envd: sandbox.EnvdMetadata{
			Vars:        map[string]string{"HELLO": "WORLD"},
			AccessToken: &accessToken,
			Version:     "1.2.3",
		},
		FirecrackerConfig: fc.Config{
			KernelVersion:      kernelVersion,
			FirecrackerVersion: fcVersion,
		},
	})

	// Template should already be cached locally
	tmpl, err := templateCache.GetTemplate(b.Context(), buildID, false, false)
	require.NoError(b, err, "template not found in cache - was it built previously?")

	// warm-up
	b.Log("warming up: creating one sandbox to prime caches...")
	warmupRuntime := sandbox.RuntimeMetadata{
		TemplateID:  templateID,
		SandboxID:   "warmup-" + uuid.NewString()[:8],
		ExecutionID: "warmup-exec",
		TeamID:      "bench-team",
	}
	warmupSbx, err := sandboxFactory.ResumeSandbox(
		b.Context(), tmpl, sandboxConfig, warmupRuntime,
		time.Now(), time.Now().Add(2*time.Minute), nil,
	)
	require.NoError(b, err, "warm-up sandbox creation failed")
	require.NoError(b, warmupSbx.Close(b.Context()))
	b.Log("warm-up complete")

	// run sub-benchmarks per concurrency level
	for _, n := range levels {
		b.Run(fmt.Sprintf("concurrency-%d", n), func(b *testing.B) {
			var allLatencies []time.Duration
			var allWallClocks []time.Duration
			var totalOK, totalFail int

			for b.Loop() {
				results, wall := runRealConcurrentResume(b, sandboxFactory, tmpl, sandboxConfig, templateID, n)
				allWallClocks = append(allWallClocks, wall)

				for _, r := range results {
					if r.err != nil {
						totalFail++
						b.Logf("  FAIL sandbox %s: %v", r.sandboxID, r.err)
					} else {
						totalOK++
						allLatencies = append(allLatencies, r.latency)
					}
				}
			}

			reportRealResults(b, n, allLatencies, allWallClocks, totalOK, totalFail)
		})
	}
}

func parseRealConcurrencyLevels() []int {
	env := os.Getenv("CONCURRENCY_LEVELS")
	if env == "" {
		return []int{1, 2, 5, 10}
	}

	parts := strings.Split(env, ",")
	levels := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			continue
		}
		levels = append(levels, n)
	}

	if len(levels) == 0 {
		return []int{1, 2, 5, 10}
	}
	slices.Sort(levels)
	return levels
}

type realConcurrencyResult struct {
	sandboxID string
	latency   time.Duration
	err       error
}

func runRealConcurrentResume(
	b *testing.B,
	factory *sandbox.Factory,
	tmpl template.Template,
	config *sandbox.Config,
	templateID string,
	n int,
) ([]realConcurrencyResult, time.Duration) {
	b.Helper()

	results := make([]realConcurrencyResult, n)
	created := make([]*sandbox.Sandbox, n)

	gate := make(chan struct{})
	var wg sync.WaitGroup

	b.StopTimer()

	for i := range n {
		wg.Go(func() {
			runtime := sandbox.RuntimeMetadata{
				TemplateID:  templateID,
				SandboxID:   fmt.Sprintf("bench-%d-%s", i, uuid.NewString()[:8]),
				ExecutionID: fmt.Sprintf("bench-exec-%d", i),
				TeamID:      "bench-team",
			}

			<-gate

			start := time.Now()
			sbx, err := factory.ResumeSandbox(
				b.Context(),
				tmpl,
				config,
				runtime,
				time.Now(),
				time.Now().Add(2*time.Minute),
				nil,
			)
			elapsed := time.Since(start)

			results[i] = realConcurrencyResult{
				sandboxID: runtime.SandboxID,
				latency:   elapsed,
				err:       err,
			}
			created[i] = sbx
		})
	}

	b.StartTimer()
	wallStart := time.Now()
	close(gate)
	wg.Wait()
	wallDuration := time.Since(wallStart)
	b.StopTimer()

	for i, sbx := range created {
		if sbx != nil {
			if err := sbx.Close(b.Context()); err != nil {
				b.Logf("warning: failed to close sandbox %d (%s): %v", i, results[i].sandboxID, err)
			}
		}
	}

	b.StartTimer()
	return results, wallDuration
}

func reportRealResults(b *testing.B, concurrency int, latencies []time.Duration, wallClocks []time.Duration, totalOK, totalFail int) {
	b.Helper()

	iterations := len(wallClocks)
	b.ReportMetric(float64(totalOK)/float64(iterations), "ok/iter")
	b.ReportMetric(float64(totalFail)/float64(iterations), "fail/iter")

	if len(wallClocks) > 0 {
		var totalWall time.Duration
		for _, w := range wallClocks {
			totalWall += w
		}
		avgWall := totalWall / time.Duration(len(wallClocks))
		b.ReportMetric(float64(avgWall.Milliseconds()), "wall-clock-ms")
	}

	if len(latencies) == 0 {
		b.Logf("concurrency=%d: all sandboxes failed across %d iterations", concurrency, iterations)
		return
	}

	slices.Sort(latencies)

	var total time.Duration
	for _, l := range latencies {
		total += l
	}
	avg := total / time.Duration(len(latencies))

	p50 := percentile(latencies, 50)
	p95 := percentile(latencies, 95)
	p99 := percentile(latencies, 99)

	b.ReportMetric(float64(avg.Milliseconds()), "avg-ms")
	b.ReportMetric(float64(p50.Milliseconds()), "p50-ms")
	b.ReportMetric(float64(p95.Milliseconds()), "p95-ms")
	b.ReportMetric(float64(p99.Milliseconds()), "p99-ms")
	b.ReportMetric(float64(latencies[0].Milliseconds()), "min-ms")
	b.ReportMetric(float64(latencies[len(latencies)-1].Milliseconds()), "max-ms")

	b.Logf("=== concurrency=%d: %d ok, %d fail across %d iterations (%d samples) ===",
		concurrency, totalOK, totalFail, iterations, len(latencies))
	b.Logf("    avg=%s p50=%s p95=%s p99=%s min=%s max=%s",
		avg.Truncate(time.Millisecond),
		p50.Truncate(time.Millisecond),
		p95.Truncate(time.Millisecond),
		p99.Truncate(time.Millisecond),
		latencies[0].Truncate(time.Millisecond),
		latencies[len(latencies)-1].Truncate(time.Millisecond),
	)
}
