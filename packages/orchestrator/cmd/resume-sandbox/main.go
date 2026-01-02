// resume-sandbox resumes a sandbox from a built template.
// Example: sudo go run ./cmd/resume-sandbox -local -build <uuid>
// Benchmark: sudo go run ./cmd/resume-sandbox -local -build <uuid> -benchmark 10
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"time"

	ldclient "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-server-sdk/v7/ldcomponents"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func main() {
	buildID := flag.String("build", "", "build ID (UUID, required)")
	kernel := flag.String("kernel", "vmlinux-6.1.102", "kernel version")
	fcVer := flag.String("firecracker", "v1.12.1_717921c", "firecracker version")
	local := flag.Bool("local", false, "use local storage")
	dataDir := flag.String("data-dir", ".local-build", "data directory for local mode")
	vcpu := flag.Int64("vcpu", 2, "vCPUs")
	memory := flag.Int64("memory", 512, "memory MB")
	disk := flag.Int64("disk", 2048, "disk MB")
	benchmark := flag.Int("benchmark", 0, "run N benchmark iterations (0 = interactive mode)")
	flag.Parse()

	if *buildID == "" {
		log.Fatal("-build required")
	}
	if os.Geteuid() != 0 {
		log.Fatal("run as root")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; fmt.Println("\n🛑 Stopping..."); cancel() }()

	if *local {
		if err := setupLocal(*dataDir); err != nil {
			log.Fatal(err)
		}
	}

	if err := run(ctx, *buildID, *kernel, *fcVer, *vcpu, *memory, *disk, *benchmark); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func setupLocal(dataDir string) error {
	abs := func(s string) string { return utils.Must(filepath.Abs(s)) }
	for _, d := range []string{"kernels", "templates", "sandbox", "orchestrator", "snapshot-cache", "fc-versions", "envd"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o755); err != nil {
			return err
		}
	}
	for _, d := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
		if err := os.MkdirAll(filepath.Join(dataDir, "orchestrator", d), 0o755); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{
		"ARTIFACTS_REGISTRY_PROVIDER":      "Local",
		"FIRECRACKER_VERSIONS_DIR":         abs(filepath.Join(dataDir, "fc-versions")),
		"HOST_ENVD_PATH":                   abs(filepath.Join(dataDir, "envd", "envd")),
		"HOST_KERNELS_DIR":                 abs(filepath.Join(dataDir, "kernels")),
		"LOCAL_TEMPLATE_STORAGE_BASE_PATH": abs(filepath.Join(dataDir, "templates")),
		"ORCHESTRATOR_BASE_PATH":           abs(filepath.Join(dataDir, "orchestrator")),
		"SANDBOX_DIR":                      abs(filepath.Join(dataDir, "sandbox")),
		"SNAPSHOT_CACHE_DIR":               abs(filepath.Join(dataDir, "snapshot-cache")),
		"STORAGE_PROVIDER":                 "Local",
		"USE_LOCAL_NAMESPACE_STORAGE":      "true",
	} {
		os.Setenv(k, v)
	}
	return nil
}

type runner struct {
	ctx       context.Context
	factory   *sandbox.Factory
	tmpl      template.Template
	sbxConfig sandbox.Config
	buildID   string
}

func (r *runner) resumeOnce(iter int) (time.Duration, error) {
	runtime := sandbox.RuntimeMetadata{
		TemplateID: r.buildID, TeamID: "local",
		SandboxID:   fmt.Sprintf("sbx-%d-%d", time.Now().UnixNano(), iter),
		ExecutionID: fmt.Sprintf("exec-%d-%d", time.Now().UnixNano(), iter),
	}

	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(r.ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	dur := time.Since(t0)
	if err != nil {
		return dur, err
	}
	sbx.Close(context.Background())
	return dur, nil
}

func (r *runner) runInteractive() error {
	runtime := sandbox.RuntimeMetadata{
		TemplateID: r.buildID, TeamID: "local",
		SandboxID:   fmt.Sprintf("sbx-%d", time.Now().UnixNano()),
		ExecutionID: fmt.Sprintf("exec-%d", time.Now().UnixNano()),
	}

	fmt.Println("🚀 Starting...")
	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(r.ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	if err != nil {
		return err
	}

	fmt.Printf("✅ Running (resumed in %s)\n", time.Since(t0))
	fmt.Printf("   sudo nsenter --net=/var/run/netns/%s ssh -o StrictHostKeyChecking=no root@169.254.0.21\n", sbx.Slot.NamespaceID())
	fmt.Println("Ctrl+C to stop")

	<-r.ctx.Done()
	fmt.Println("🧹 Cleanup...")
	sbx.Close(context.Background())
	return nil
}

func (r *runner) runBenchmark(count int) error {
	var durations []time.Duration

	for i := 0; i < count; i++ {
		if r.ctx.Err() != nil {
			break
		}
		fmt.Printf("🚀 [%d/%d] Starting...\n", i+1, count)
		dur, err := r.resumeOnce(i)
		if err != nil {
			return err
		}
		durations = append(durations, dur)
		fmt.Printf("✅ [%d/%d] Resumed in %s\n", i+1, count, dur)
	}

	printStats(durations)
	return nil
}

func printStats(durations []time.Duration) {
	if len(durations) == 0 {
		return
	}

	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	n := len(sorted)
	fmt.Printf("\n📊 Results (%d runs):\n", n)
	fmt.Printf("   Min:  %s\n", sorted[0])
	fmt.Printf("   Max:  %s\n", sorted[n-1])
	fmt.Printf("   Avg:  %s\n", total/time.Duration(n))
	fmt.Printf("   P95:  %s\n", percentile(sorted, 0.95))
	fmt.Printf("   P99:  %s\n", percentile(sorted, 0.99))
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func run(ctx context.Context, buildID, kernel, fcVer string, vcpu, memory, disk int64, count int) error {
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
	defer networkPool.Close(context.Background())

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("nbd pool (modprobe nbd?): %w", err)
	}
	go devicePool.Populate(ctx)
	defer devicePool.Close(context.Background())

	ldClient, _ := ldclient.MakeCustomClient("", ldclient.Config{
		DataSource: ldtestdata.DataSource(),
		Logging:    ldcomponents.NoLogging(),
	}, 0)
	defer ldClient.Close()
	flags := featureflags.WrapLDClient(ldClient)

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

	token := "local"
	r := &runner{
		ctx:     ctx,
		factory: factory,
		tmpl:    tmpl,
		buildID: buildID,
		sbxConfig: sandbox.Config{
			BaseTemplateID: buildID, Vcpu: vcpu, RamMB: memory, TotalDiskSizeMB: disk,
			Network:           &orchestrator.SandboxNetworkConfig{},
			Envd:              sandbox.EnvdMetadata{Vars: map[string]string{}, AccessToken: &token, Version: "1.0.0"},
			FirecrackerConfig: fc.Config{KernelVersion: kernel, FirecrackerVersion: fcVer},
		},
	}

	if count > 0 {
		return r.runBenchmark(count)
	}
	return r.runInteractive()
}
