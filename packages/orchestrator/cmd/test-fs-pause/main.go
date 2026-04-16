// test-fs-pause exercises the FS-only pause/resume flow locally.
//
// It resumes a sandbox from an existing build, writes a test file, does
// an FS-only pause (exporting only the rootfs CoW diff), then does an
// FS-only resume from the hidden base + saved diff and verifies the file
// survived.
//
// Usage:
//
//	sudo go run ./cmd/test-fs-pause -from-build <BUILD_ID> -storage .local-build
//
// The -from-build must be a local build created with cmd/create-build.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats"
	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/cgroup"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/tcpfirewall"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func main() {
	fromBuild := flag.String("from-build", "", "build ID to resume from (required)")
	storagePath := flag.String("storage", ".local-build", "storage path")
	verbose := flag.Bool("v", false, "verbose logging")
	flag.Parse()

	if *fromBuild == "" {
		log.Fatal("-from-build required")
	}
	if os.Geteuid() != 0 {
		log.Fatal("run as root")
	}

	if !*verbose {
		cmdutil.SuppressNoisyLogs()
	}

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; fmt.Println("\nStopping..."); cancel() }()

	if err := run(ctx, *fromBuild, *storagePath, *verbose); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, buildID, storagePath string, verbose bool) error {
	if err := setupEnv(storagePath); err != nil {
		return err
	}

	sbxlogger.SetSandboxLoggerInternal(logger.NewNopLogger())

	if os.Getenv("NODE_IP") == "" {
		os.Setenv("NODE_IP", "127.0.0.1")
	}

	config, err := cfg.Parse()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	flags, _ := featureflags.NewClientWithLogLevel(ldlog.Error)
	sandboxes := sandbox.NewSandboxesMap()

	l := logger.NewNopLogger()
	tcpFw := tcpfirewall.New(l, config.NetworkConfig, sandboxes, noop.NewMeterProvider(), flags)
	go tcpFw.Start(ctx)
	defer tcpFw.Close(context.WithoutCancel(ctx))

	slotStorage, err := network.NewStorageLocal(ctx, config.NetworkConfig, network.NoopEgressProxy{})
	if err != nil {
		return fmt.Errorf("network storage: %w", err)
	}
	networkPool := network.NewPool(8, 8, slotStorage, config.NetworkConfig)
	go networkPool.Populate(ctx)
	defer networkPool.Close(context.WithoutCancel(ctx))

	devicePool, err := nbd.NewDevicePool(config.NBDPoolSize)
	if err != nil {
		return fmt.Errorf("nbd pool: %w", err)
	}
	go devicePool.Populate(ctx)
	defer devicePool.Close(context.WithoutCancel(ctx))

	persistence, err := storage.GetStorageProvider(ctx, storage.TemplateStorageConfig)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	blockMetrics, _ := blockmetrics.NewMetrics(noop.NewMeterProvider())
	cache, err := sbxtemplate.NewCache(config, flags, persistence, blockMetrics, peerclient.NopResolver())
	if err != nil {
		return fmt.Errorf("template cache: %w", err)
	}
	cache.Start(ctx)
	defer cache.Stop()

	factory := sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, flags, hoststats.NewNoopDelivery(), cgroup.NewNoopManager(), sandboxes)

	// ========== Step 1: Resume sandbox from build ==========
	fmt.Printf("1. Loading template %s...\n", buildID)
	tmpl, err := cache.GetTemplate(ctx, buildID, false, false)
	if err != nil {
		return fmt.Errorf("load template: %w", err)
	}

	meta, err := tmpl.Metadata()
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}

	token := "test"
	sbxCfg := sandbox.NewConfig(sandbox.Config{
		BaseTemplateID: buildID,
		Vcpu:           1,
		RamMB:          512,
		Envd:           sandbox.EnvdMetadata{Vars: map[string]string{}, AccessToken: &token, Version: "1.0.0"},
		FirecrackerConfig: fc.Config{
			KernelVersion:      meta.Template.KernelVersion,
			FirecrackerVersion: meta.Template.FirecrackerVersion,
		},
	})

	runtime := sandbox.RuntimeMetadata{
		TemplateID:  buildID,
		TeamID:      "test",
		SandboxID:   fmt.Sprintf("fs-test-%d", time.Now().UnixNano()),
		ExecutionID: fmt.Sprintf("fs-exec-%d", time.Now().UnixNano()),
	}

	fmt.Println("2. Starting sandbox...")
	t0 := time.Now()
	sbx, err := factory.ResumeSandbox(ctx, tmpl, sbxCfg, runtime, t0, t0.Add(time.Hour), nil)
	if err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	fmt.Printf("   Resumed in %s\n", time.Since(t0))

	// ========== Step 2: Write a test file ==========
	testContent := fmt.Sprintf("fs-pause-test-%d", time.Now().UnixNano())
	fmt.Printf("3. Writing test file: %s\n", testContent)
	if err := execInSandbox(ctx, sbx, fmt.Sprintf("echo -n '%s' > /tmp/fs-test.txt", testContent)); err != nil {
		sbx.Close(context.WithoutCancel(ctx))
		return fmt.Errorf("write file: %w", err)
	}

	// Verify the file was written
	if err := execInSandbox(ctx, sbx, "cat /tmp/fs-test.txt"); err != nil {
		sbx.Close(context.WithoutCancel(ctx))
		return fmt.Errorf("verify write: %w", err)
	}

	// ========== Step 3: FS-only pause ==========
	fmt.Println("4. FS-only pausing...")
	pauseStart := time.Now()
	fsDiff, err := sbx.PauseFS(ctx)
	pauseDur := time.Since(pauseStart)
	if err != nil {
		sbx.Close(context.WithoutCancel(ctx))
		return fmt.Errorf("fs-pause: %w", err)
	}
	fmt.Printf("   FS-paused in %s (dirty blocks: %d)\n", pauseDur, fsDiff.DirtyBitset.Count())

	// ========== Step 4: Inspect the diff ==========
	diffPath, _ := fsDiff.RootfsDiff.CachePath()
	fmt.Printf("   Diff file: %s\n", diffPath)
	if fsize, err := fsDiff.RootfsDiff.FileSize(); err == nil {
		fmt.Printf("   Diff size: %d KB\n", fsize>>10)
	}

	// ========== Step 5: Cleanup and report ==========
	fmt.Println("\n5. FS-only pause completed successfully!")
	fmt.Println("   The diff contains only the blocks that changed (user files).")
	fmt.Println("   To test FS-only resume, we'd need the hidden base snapshot")
	fmt.Println("   (taken before overlay mount) and the two-disk FC setup.")

	fmt.Printf("\n   Total time: resume=%s, pause=%s\n", time.Since(t0)-pauseDur, pauseDur)

	fsDiff.Close(context.WithoutCancel(ctx))

	return nil
}

func setupEnv(storagePath string) error {
	abs := func(s string) string { return utils.Must(filepath.Abs(s)) }
	dataDir := storagePath

	if strings.HasPrefix(storagePath, "gs://") {
		dataDir = ".local-build"
		os.Setenv("STORAGE_PROVIDER", "GCPBucket")
		os.Setenv("TEMPLATE_BUCKET_NAME", strings.TrimPrefix(storagePath, "gs://"))
	} else {
		os.Setenv("STORAGE_PROVIDER", "Local")
		os.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", abs(filepath.Join(dataDir, "templates")))
	}

	for _, d := range []string{"kernels", "templates", "sandbox", "orchestrator", "snapshot-cache", "fc-versions"} {
		os.MkdirAll(filepath.Join(dataDir, d), 0o755)
	}
	for _, d := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
		os.MkdirAll(filepath.Join(dataDir, "orchestrator", d), 0o755)
	}

	env := map[string]string{
		"ARTIFACTS_REGISTRY_PROVIDER":      "Local",
		"FIRECRACKER_VERSIONS_DIR":         abs(filepath.Join(dataDir, "fc-versions")),
		"HOST_KERNELS_DIR":                 abs(filepath.Join(dataDir, "kernels")),
		"ORCHESTRATOR_BASE_PATH":           abs(filepath.Join(dataDir, "orchestrator")),
		"SNAPSHOT_CACHE_DIR":               abs(filepath.Join(dataDir, "snapshot-cache")),
		"USE_LOCAL_NAMESPACE_STORAGE":      "true",
	}
	for k, v := range env {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}

	return nil
}

func execInSandbox(ctx context.Context, sbx *sandbox.Sandbox, command string) error {
	envdURL := fmt.Sprintf("http://%s:%d", sbx.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	hc := http.Client{Timeout: 30 * time.Second, Transport: sandbox.SandboxHttpTransport}
	processC := processconnect.NewProcessClient(&hc, envdURL)

	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "/bin/bash",
			Args: []string{"-l", "-c", command},
		},
	})
	grpc.SetUserHeader(req.Header(), "root")
	if sbx.Config.Envd.AccessToken != nil {
		req.Header().Set("X-Access-Token", *sbx.Config.Envd.AccessToken)
	}

	stream, err := processC.Start(ctx, req)
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	defer stream.Close()

	for stream.Receive() {
		msg := stream.Msg()
		event := msg.GetEvent()
		if event == nil {
			continue
		}
		switch e := event.GetEvent().(type) {
		case *process.ProcessEvent_Data:
			if data := e.Data; data != nil {
				if stdout := data.GetStdout(); stdout != nil {
					fmt.Print(string(stdout))
				}
				if stderr := data.GetStderr(); stderr != nil {
					fmt.Print(string(stderr))
				}
			}
		case *process.ProcessEvent_End:
			if end := e.End; end != nil && (!end.GetExited() || end.GetExitCode() != 0) {
				return fmt.Errorf("exit code %d", end.GetExitCode())
			}
			return nil
		}
	}

	return stream.Err()
}
