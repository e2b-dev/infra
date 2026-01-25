package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func main() {
	buildID := flag.String("build", "", "build ID (UUID)")
	from := flag.String("from", ".local-build", "template source: local path or gs://bucket")
	iterations := flag.Int("iterations", 0, "run N iterations (0 = interactive)")
	coldStart := flag.Bool("cold", false, "clear cache between iterations (cold start each time)")
	noPrefetch := flag.Bool("no-prefetch", false, "disable memory prefetching")
	verbose := flag.Bool("v", false, "verbose logging")

	flag.Parse()

	if *buildID == "" {
		log.Fatal("-build required")
	}

	if os.Geteuid() != 0 {
		log.Fatal("run as root")
	}

	if err := setupEnv(*from); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; fmt.Println("\n🛑 Stopping..."); cancel() }()

	err := run(ctx, *buildID, *iterations, *coldStart, *noPrefetch, *verbose)
	cancel()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "(context: %v)\n", ctx.Err())
		}
		os.Exit(1)
	}
}

func setupEnv(from string) error {
	abs := func(s string) string { return utils.Must(filepath.Abs(s)) }

	// Derive dataDir from 'from' when it's a local path
	var dataDir string
	if strings.HasPrefix(from, "gs://") {
		dataDir = ".local-build"
	} else {
		dataDir = from
	}

	for _, d := range []string{"kernels", "templates", "sandbox", "orchestrator", "snapshot-cache", "fc-versions", "envd"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	for _, d := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
		if err := os.MkdirAll(filepath.Join(dataDir, "orchestrator", d), 0o755); err != nil {
			return fmt.Errorf("mkdir orchestrator/%s: %w", d, err)
		}
	}

	env := map[string]string{
		"ARTIFACTS_REGISTRY_PROVIDER": "Local",
		"FIRECRACKER_VERSIONS_DIR":    abs(filepath.Join(dataDir, "fc-versions")),
		"HOST_ENVD_PATH":              abs(filepath.Join(dataDir, "envd", "envd")),
		"HOST_KERNELS_DIR":            abs(filepath.Join(dataDir, "kernels")),
		"ORCHESTRATOR_BASE_PATH":      abs(filepath.Join(dataDir, "orchestrator")),
		"SANDBOX_DIR":                 abs(filepath.Join(dataDir, "sandbox")),
		"SNAPSHOT_CACHE_DIR":          abs(filepath.Join(dataDir, "snapshot-cache")),
		"USE_LOCAL_NAMESPACE_STORAGE": "true",
	}

	if strings.HasPrefix(from, "gs://") {
		env["STORAGE_PROVIDER"] = "GCPBucket"
		env["TEMPLATE_BUCKET_NAME"] = strings.TrimPrefix(from, "gs://")
	} else {
		env["STORAGE_PROVIDER"] = "Local"
		env["LOCAL_TEMPLATE_STORAGE_BASE_PATH"] = abs(filepath.Join(dataDir, "templates"))
	}

	for k, v := range env {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}

	return nil
}

type runner struct {
	factory    *sandbox.Factory
	tmpl       template.Template
	sbxConfig  sandbox.Config
	buildID    string
	cache      *template.Cache
	coldStart  bool
	noPrefetch bool
}

func (r *runner) resumeOnce(ctx context.Context, iter int) (time.Duration, error) {
	runtime := sandbox.RuntimeMetadata{
		TemplateID:  r.buildID,
		TeamID:      "local",
		SandboxID:   fmt.Sprintf("sbx-%d-%d", time.Now().UnixNano(), iter),
		ExecutionID: fmt.Sprintf("exec-%d-%d", time.Now().UnixNano(), iter),
	}

	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	dur := time.Since(t0)

	if sbx != nil {
		sbx.Close(context.WithoutCancel(ctx))
	}

	return dur, err
}

func (r *runner) interactive(ctx context.Context) error {
	runtime := sandbox.RuntimeMetadata{
		TemplateID:  r.buildID,
		TeamID:      "local",
		SandboxID:   fmt.Sprintf("sbx-%d", time.Now().UnixNano()),
		ExecutionID: fmt.Sprintf("exec-%d", time.Now().UnixNano()),
	}

	fmt.Println("🚀 Starting...")
	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	if err != nil {
		return err
	}

	fmt.Printf("✅ Running (resumed in %s)\n", time.Since(t0))
	fmt.Printf("   sudo nsenter --net=/var/run/netns/%s ssh -o StrictHostKeyChecking=no root@169.254.0.21\n", sbx.Slot.NamespaceID())
	fmt.Println("Ctrl+C to stop")

	<-ctx.Done()
	fmt.Println("🧹 Cleanup...")
	sbx.Close(context.WithoutCancel(ctx))

	return nil
}

func (r *runner) benchmark(ctx context.Context, n int) error {
	results := make([]benchResult, 0, n)
	var lastErr error

	for i := range n {
		if ctx.Err() != nil {
			break
		}

		// Clear all caches for cold start
		if r.coldStart && i > 0 {
			r.cache.InvalidateAll()
			if err := dropPageCache(); err != nil {
				return fmt.Errorf("drop page cache: %w", err)
			}
			tmpl, err := r.cache.GetTemplate(ctx, r.buildID, false, false)
			if err != nil {
				return fmt.Errorf("reload template: %w", err)
			}
			if r.noPrefetch {
				tmpl = &noPrefetchTemplate{tmpl}
			}
			r.tmpl = tmpl
		}

		fmt.Printf("\r[%d/%d] Running...    ", i+1, n)
		dur, err := r.resumeOnce(ctx, i)
		results = append(results, benchResult{dur, err})

		if err != nil {
			fmt.Printf("\r[%d/%d] ❌ Failed\n", i+1, n)
			lastErr = err

			break
		}
	}
	fmt.Print("\r                    \r") // Clear progress line

	printResults(results)

	return lastErr
}

func run(ctx context.Context, buildID string, iterations int, coldStart, noPrefetch, verbose bool) error {
	// Silence loggers unless verbose mode
	if !verbose {
		log.SetOutput(io.Discard)
	}
	sbxlogger.SetSandboxLoggerInternal(logger.NewNopLogger())

	if verbose {
		fmt.Println("🔧 Parsing config...")
	}
	config, err := cfg.Parse()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	if verbose {
		fmt.Println("🔧 Creating network storage...")
	}
	slotStorage, err := network.NewStorageLocal(ctx, config.NetworkConfig)
	if err != nil {
		return fmt.Errorf("network storage: %w", err)
	}

	if verbose {
		fmt.Println("🔧 Creating network pool...")
	}
	networkPool := network.NewPool(8, 8, slotStorage, config.NetworkConfig)
	go networkPool.Populate(ctx)
	defer networkPool.Close(context.WithoutCancel(ctx))

	if verbose {
		fmt.Println("🔧 Creating NBD device pool...")
	}
	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("nbd pool: %w", err)
	}
	go devicePool.Populate(ctx)
	defer devicePool.Close(context.WithoutCancel(ctx))

	if verbose {
		fmt.Println("🔧 Creating feature flags client...")
	}
	flags, _ := featureflags.NewClient()

	if verbose {
		fmt.Println("🔧 Creating storage provider...")
	}
	persistence, err := storage.ForTemplates(ctx, nil)
	if verbose {
		fmt.Println("🔧 Storage provider created, err:", err)
	}
	if err != nil {
		return fmt.Errorf("storage provider: %w", err)
	}
	if persistence == nil {
		return fmt.Errorf("storage provider is nil")
	}

	if verbose {
		fmt.Println("🔧 Creating block metrics...")
	}
	blockMetrics, _ := blockmetrics.NewMetrics(&noop.MeterProvider{})

	if verbose {
		fmt.Println("🔧 Creating template cache...")
	}
	cache, err := template.NewCache(config, flags, persistence, blockMetrics)
	if err != nil {
		return fmt.Errorf("template cache: %w", err)
	}
	cache.Start(ctx)
	defer cache.Stop()

	if verbose {
		fmt.Println("🔧 Creating sandbox factory...")
	}
	factory := sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, flags)

	fmt.Printf("📦 Loading %s...\n", buildID)
	tmpl, err := cache.GetTemplate(ctx, buildID, false, false)
	if err != nil {
		return err
	}

	meta, err := tmpl.Metadata()
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}

	printTemplateInfo(ctx, tmpl, meta)

	// Wrap template to disable prefetching if requested
	if noPrefetch {
		tmpl = &noPrefetchTemplate{tmpl}
		fmt.Println("   Prefetch: disabled")
	}

	token := "local"
	r := &runner{
		factory:    factory,
		tmpl:       tmpl,
		buildID:    buildID,
		cache:      cache,
		coldStart:  coldStart,
		noPrefetch: noPrefetch,
		sbxConfig: sandbox.Config{
			BaseTemplateID: buildID,
			Vcpu:           1,
			RamMB:          512,
			Network:        &orchestrator.SandboxNetworkConfig{},
			Envd:           sandbox.EnvdMetadata{Vars: map[string]string{}, AccessToken: &token, Version: "1.0.0"},
			FirecrackerConfig: fc.Config{
				KernelVersion:      meta.Template.KernelVersion,
				FirecrackerVersion: meta.Template.FirecrackerVersion,
			},
		},
	}

	if iterations > 0 {
		return r.benchmark(ctx, iterations)
	}

	return r.interactive(ctx)
}

func printTemplateInfo(ctx context.Context, tmpl template.Template, meta metadata.Template) {
	fmt.Printf("   Kernel: %s, Firecracker: %s\n", meta.Template.KernelVersion, meta.Template.FirecrackerVersion)

	if memfile, err := tmpl.Memfile(ctx); err == nil {
		if size, err := memfile.Size(ctx); err == nil {
			fmt.Printf("   Memfile: %d MB (%d KB blocks)\n", size>>20, memfile.BlockSize()>>10)
		}
	}

	if rootfs, err := tmpl.Rootfs(); err == nil {
		if size, err := rootfs.Size(ctx); err == nil {
			fmt.Printf("   Rootfs: %d MB (%d KB blocks)\n", size>>20, rootfs.BlockSize()>>10)
		}
	}

	if meta.Prefetch != nil && meta.Prefetch.Memory != nil {
		fmt.Printf("   Prefetch: %d blocks\n", meta.Prefetch.Memory.Count())
	}
}

// Benchmark output formatting

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
)

type benchResult struct {
	dur time.Duration
	err error
}

func fmtDur(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)

	return fmt.Sprintf("%.1fms", ms)
}

// dropPageCache drops the OS page cache to simulate cold starts.
// This ensures files aren't served from memory on subsequent runs.
func dropPageCache() error {
	// Sync first to flush dirty pages
	unix.Sync()

	// Drop page cache (requires root)
	// 3 = free pagecache, dentries, and inodes
	return os.WriteFile("/proc/sys/vm/drop_caches", []byte("3"), 0o644)
}

func printResults(results []benchResult) {
	if len(results) == 0 {
		return
	}

	// Calculate average
	var total time.Duration
	var successCount int
	for _, r := range results {
		if r.err == nil {
			total += r.dur
			successCount++
		}
	}

	if successCount == 0 {
		fmt.Println("\n❌ All runs failed")

		return
	}

	avg := total / time.Duration(successCount)

	// Print individual results
	fmt.Printf("\n📋 Run times:\n")
	for i, r := range results {
		if r.err != nil {
			fmt.Printf("   [%2d] ❌ Failed: %v\n", i+1, r.err)

			continue
		}

		diff := r.dur - avg
		pct := float64(diff) / float64(avg) * 100

		var color string
		switch {
		case diff < 0:
			color = colorGreen
		case diff > 0:
			color = colorRed
		default:
			color = colorYellow
		}

		fmt.Printf("   [%2d] %s  %s%+.1f%%%s\n", i+1, fmtDur(r.dur), color, pct, colorReset)
	}

	// Print summary stats
	durations := make([]time.Duration, 0, successCount)
	for _, r := range results {
		if r.err == nil {
			durations = append(durations, r.dur)
		}
	}

	sorted := slices.Clone(durations)
	slices.Sort(sorted)

	// Calculate standard deviation
	var variance float64
	avgFloat := float64(avg)
	for _, d := range durations {
		diff := float64(d) - avgFloat
		variance += diff * diff
	}
	variance /= float64(len(durations))
	stdDev := time.Duration(math.Sqrt(variance))

	n := len(sorted)
	fmt.Printf("\n📊 Summary (%d runs):\n", n)
	fmt.Printf("   Min: %s | Max: %s | Avg: %s | StdDev: %s\n", fmtDur(sorted[0]), fmtDur(sorted[n-1]), fmtDur(avg), fmtDur(stdDev))
	if n > 1 {
		p95idx := int(float64(n-1) * 0.95)
		p99idx := int(float64(n-1) * 0.99)
		fmt.Printf("   P95: %s | P99: %s\n", fmtDur(sorted[p95idx]), fmtDur(sorted[p99idx]))
	}
}

// noPrefetchTemplate wraps a template to disable prefetching by returning nil Prefetch in metadata.
type noPrefetchTemplate struct {
	template.Template
}

func (t *noPrefetchTemplate) Metadata() (metadata.Template, error) {
	meta, err := t.Template.Metadata()
	if err != nil {
		return meta, err
	}
	meta.Prefetch = nil

	return meta, nil
}
