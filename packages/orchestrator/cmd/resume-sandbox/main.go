package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

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

	err := run(ctx, *buildID, *iterations)
	cancel()

	if err != nil && ctx.Err() == nil {
		log.Fatal(err)
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
		os.Setenv(k, v)
	}

	return nil
}

type runner struct {
	factory   *sandbox.Factory
	tmpl      template.Template
	sbxConfig sandbox.Config
	buildID   string
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
	var durations []time.Duration
	var lastErr error

	for i := range n {
		if ctx.Err() != nil {
			break
		}

		fmt.Printf("[%d/%d] Starting...\n", i+1, n)
		dur, err := r.resumeOnce(ctx, i)
		durations = append(durations, dur)

		if err != nil {
			fmt.Printf("[%d/%d] ❌ Failed after %s: %v\n", i+1, n, dur, err)
			lastErr = err

			break
		}

		fmt.Printf("[%d/%d] Resumed in %s\n", i+1, n, dur)
	}

	printStats(durations)

	return lastErr
}

func printStats(durations []time.Duration) {
	if len(durations) == 0 {
		return
	}

	sorted := slices.Clone(durations)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	n := len(sorted)
	fmt.Printf("\n📊 Results (%d runs):\n", n)
	fmt.Printf("   Min: %s | Max: %s | Avg: %s\n", sorted[0], sorted[n-1], total/time.Duration(n))
	fmt.Printf("   P95: %s | P99: %s\n", sorted[int(float64(n-1)*0.95)], sorted[int(float64(n-1)*0.99)])
}

func run(ctx context.Context, buildID string, iterations int) error {
	l, _ := logger.NewDevelopmentLogger()
	sbxlogger.SetSandboxLoggerInternal(l)

	config, err := cfg.Parse()
	if err != nil {
		return err
	}

	slotStorage, err := network.NewStorageLocal(ctx, config.NetworkConfig)
	if err != nil {
		return err
	}

	networkPool := network.NewPool(8, 8, slotStorage, config.NetworkConfig)
	go networkPool.Populate(ctx)
	defer networkPool.Close(context.WithoutCancel(ctx))

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("nbd pool: %w", err)
	}
	go devicePool.Populate(ctx)
	defer devicePool.Close(context.WithoutCancel(ctx))

	flags, _ := featureflags.NewClient()
	persistence, _ := storage.GetTemplateStorageProvider(ctx, nil)
	blockMetrics, _ := blockmetrics.NewMetrics(&noop.MeterProvider{})

	cache, err := template.NewCache(config, flags, persistence, blockMetrics)
	if err != nil {
		return err
	}
	cache.Start(ctx)
	defer cache.Stop()

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

	token := "local"
	r := &runner{
		factory: factory,
		tmpl:    tmpl,
		buildID: buildID,
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
		if size, err := memfile.Size(); err == nil {
			fmt.Printf("   Memfile: %d MB (%d KB blocks)\n", size>>20, memfile.BlockSize()>>10)
		}
	}

	if rootfs, err := tmpl.Rootfs(); err == nil {
		if size, err := rootfs.Size(); err == nil {
			fmt.Printf("   Rootfs: %d MB (%d KB blocks)\n", size>>20, rootfs.BlockSize()>>10)
		}
	}

	if meta.Prefetch != nil && meta.Prefetch.Memory != nil {
		fmt.Printf("   Prefetch: %d blocks\n", meta.Prefetch.Memory.Count())
	}
}
