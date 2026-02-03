package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/tcpfirewall"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func main() {
	fromBuild := flag.String("from-build", "", "build ID (UUID) to resume from (required)")
	toBuild := flag.String("to-build", "", "output build ID (UUID) for pause snapshot (auto-generated if not specified)")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	iterations := flag.Int("iterations", 0, "run N iterations (0 = interactive)")
	coldStart := flag.Bool("cold", false, "clear cache between iterations (cold start each time)")
	noPrefetch := flag.Bool("no-prefetch", false, "disable memory prefetching")
	verbose := flag.Bool("v", false, "verbose logging")

	// Command execution (no pause)
	cmd := flag.String("cmd", "", "execute command in sandbox and exit (no snapshot)")

	// Pause flags
	pause := flag.Bool("pause", false, "start and immediately pause (snapshot)")
	signalPause := flag.String("signal-pause", "", "wait for signal before pause (e.g., SIGTERM, SIGUSR1)")
	cmdPause := flag.String("cmd-pause", "", "execute command in sandbox, then pause on success")

	flag.Parse()

	if *fromBuild == "" {
		log.Fatal("-from-build required")
	}

	if os.Geteuid() != 0 {
		log.Fatal("run as root")
	}

	// Count pause options - only one allowed
	pauseCount := 0
	if *pause {
		pauseCount++
	}
	if *signalPause != "" {
		pauseCount++
	}
	if *cmdPause != "" {
		pauseCount++
	}
	if pauseCount > 1 {
		log.Fatal("only one of -pause, -signal-pause, or -cmd-pause can be specified")
	}

	// -cmd is incompatible with pause flags
	isCmdMode := *cmd != ""
	if isCmdMode && pauseCount > 0 {
		log.Fatal("-cmd is incompatible with pause flags")
	}

	isPauseMode := pauseCount > 0

	// -signal-pause and -cmd-pause are incompatible with iterations (they require interaction)
	if *iterations > 0 && (*signalPause != "" || *cmdPause != "") {
		log.Fatal("-signal-pause and -cmd-pause are incompatible with -iterations")
	}

	// -to-build only makes sense with pause
	if *toBuild != "" && !isPauseMode {
		log.Fatal("-to-build requires a pause flag (-pause, -signal-pause, or -cmd-pause)")
	}

	// Generate new build ID if not specified and pause mode is enabled
	outputBuildID := *toBuild
	if isPauseMode && outputBuildID == "" {
		outputBuildID = uuid.New().String()
	}

	if err := setupEnv(*storagePath); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; fmt.Println("\n🛑 Stopping..."); cancel() }()

	isRemoteStorage := strings.HasPrefix(*storagePath, "gs://")
	pauseOpts := pauseOptions{
		immediate:       *pause,
		signalName:      *signalPause,
		command:         *cmdPause,
		storagePath:     *storagePath,
		isRemoteStorage: isRemoteStorage,
		newBuildID:      outputBuildID,
		iterations:      *iterations,
	}

	runOpts := runOptions{
		cmd:        *cmd,
		iterations: *iterations,
	}

	err := run(ctx, *fromBuild, *iterations, *coldStart, *noPrefetch, *verbose, pauseOpts, runOpts)
	cancel()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "(context: %v)\n", ctx.Err())
		}
		os.Exit(1)
	}
}

type pauseOptions struct {
	immediate       bool
	signalName      string
	command         string
	storagePath     string
	isRemoteStorage bool
	newBuildID      string
	iterations      int // for benchmarking pause (only with immediate)
}

func (p pauseOptions) enabled() bool {
	return p.immediate || p.signalName != "" || p.command != ""
}

// pauseTimings holds timing breakdown for a pause operation
type pauseTimings struct {
	resume time.Duration
	pause  time.Duration
	total  time.Duration
	err    error
}

type runOptions struct {
	cmd        string // command to run and exit (no pause)
	iterations int    // number of iterations (0 = single run)
}

func (r runOptions) enabled() bool {
	return r.cmd != ""
}

// cmdTimings holds timing breakdown for a command run
type cmdTimings struct {
	resume  time.Duration
	command time.Duration
	total   time.Duration
	err     error
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
	sandboxes  *sandbox.Map
	tmpl       template.Template
	sbxConfig  sandbox.Config
	buildID    string
	cache      *template.Cache
	coldStart  bool
	noPrefetch bool
	config     cfg.BuilderConfig
	storage    storage.StorageProvider
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

	// Register sandbox in map for TCP firewall to find
	r.sandboxes.Insert(sbx)
	defer r.sandboxes.Remove(runtime.SandboxID)

	fmt.Printf("✅ Running (resumed in %s)\n", time.Since(t0))
	fmt.Printf("   sudo nsenter --net=/var/run/netns/%s ssh -o StrictHostKeyChecking=no root@169.254.0.21\n", sbx.Slot.NamespaceID())
	fmt.Println("Ctrl+C to stop")

	<-ctx.Done()
	fmt.Println("🧹 Cleanup...")
	sbx.Close(context.WithoutCancel(ctx))

	return nil
}

func (r *runner) cmdMode(ctx context.Context, opts runOptions) error {
	if opts.iterations > 0 {
		return r.cmdBenchmark(ctx, opts)
	}

	_, err := r.cmdOnce(ctx, opts, true)

	return err
}

func (r *runner) cmdOnce(ctx context.Context, opts runOptions, verbose bool) (cmdTimings, error) {
	runtime := sandbox.RuntimeMetadata{
		TemplateID:  r.buildID,
		TeamID:      "local",
		SandboxID:   fmt.Sprintf("sbx-%d", time.Now().UnixNano()),
		ExecutionID: fmt.Sprintf("exec-%d", time.Now().UnixNano()),
	}

	if verbose {
		fmt.Println("🚀 Starting sandbox...")
	}
	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	resumeDur := time.Since(t0)
	if err != nil {
		return cmdTimings{resume: resumeDur, err: err}, err
	}
	defer sbx.Close(context.WithoutCancel(ctx))

	// Register sandbox in map for TCP firewall to find
	r.sandboxes.Insert(sbx)
	defer r.sandboxes.Remove(runtime.SandboxID)

	if verbose {
		fmt.Printf("✅ Sandbox resumed in %s\n", resumeDur)
		fmt.Printf("🔧 Running: %s\n", opts.cmd)
	}

	cmdStart := time.Now()
	cmdErr := runCommandInSandbox(ctx, sbx, opts.cmd)
	cmdDur := time.Since(cmdStart)
	totalDur := time.Since(t0)

	timings := cmdTimings{
		resume:  resumeDur,
		command: cmdDur,
		total:   totalDur,
		err:     cmdErr,
	}

	if verbose {
		if cmdErr != nil {
			fmt.Printf("❌ Command failed: %v\n", cmdErr)
		} else {
			fmt.Println()
			fmt.Println("📊 Timing breakdown:")
			fmt.Printf("   Resume:  %s\n", fmtDur(resumeDur))
			fmt.Printf("   Command: %s\n", fmtDur(cmdDur))
			fmt.Printf("   Total:   %s\n", fmtDur(totalDur))
		}
	}

	return timings, cmdErr
}

func (r *runner) cmdBenchmark(ctx context.Context, opts runOptions) error {
	results := make([]cmdTimings, 0, opts.iterations)

	fmt.Printf("📦 Benchmarking command: %s\n", opts.cmd)
	fmt.Printf("   Iterations: %d, Cold start: %v\n\n", opts.iterations, r.coldStart)

	for i := range opts.iterations {
		if ctx.Err() != nil {
			break
		}

		// Clear caches for cold start
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

		fmt.Printf("\r[%d/%d] Running...    ", i+1, opts.iterations)
		timings, _ := r.cmdOnce(ctx, opts, false)
		results = append(results, timings)

		if timings.err != nil {
			fmt.Printf("\r[%d/%d] ❌ Failed: %v\n", i+1, opts.iterations, timings.err)

			break
		}
	}
	fmt.Print("\r                         \r")

	printCmdResults(results)

	// Return last error if any
	for _, t := range results {
		if t.err != nil {
			return t.err
		}
	}

	return nil
}

func printCmdResults(results []cmdTimings) {
	if len(results) == 0 {
		return
	}

	// Separate successful results
	var successful []cmdTimings
	for _, r := range results {
		if r.err == nil {
			successful = append(successful, r)
		}
	}

	if len(successful) == 0 {
		fmt.Println("\n❌ All runs failed")

		return
	}

	// Calculate averages
	var totalResume, totalCmd, totalTotal time.Duration
	for _, t := range successful {
		totalResume += t.resume
		totalCmd += t.command
		totalTotal += t.total
	}
	n := len(successful)
	avgResume := totalResume / time.Duration(n)
	avgCmd := totalCmd / time.Duration(n)
	avgTotal := totalTotal / time.Duration(n)

	// Print individual results
	fmt.Println("\n📋 Run times (resume / command / total):")
	for i, t := range results {
		if t.err != nil {
			fmt.Printf("   [%2d] ❌ Failed: %v\n", i+1, t.err)

			continue
		}

		resumeDiff := float64(t.resume-avgResume) / float64(avgResume) * 100
		cmdDiff := float64(t.command-avgCmd) / float64(avgCmd) * 100

		fmt.Printf("   [%2d] %s / %s / %s  (resume: %s%+.1f%%%s, cmd: %s%+.1f%%%s)\n",
			i+1,
			fmtDur(t.resume), fmtDur(t.command), fmtDur(t.total),
			colorForDiff(resumeDiff), resumeDiff, colorReset,
			colorForDiff(cmdDiff), cmdDiff, colorReset)
	}

	// Print summary
	fmt.Printf("\n📊 Summary (%d runs):\n", n)
	fmt.Printf("   Resume:  Avg %s\n", fmtDur(avgResume))
	fmt.Printf("   Command: Avg %s\n", fmtDur(avgCmd))
	fmt.Printf("   Total:   Avg %s\n", fmtDur(avgTotal))

	// Min/Max for each
	if n > 1 {
		minR, maxR := successful[0].resume, successful[0].resume
		minC, maxC := successful[0].command, successful[0].command
		for _, t := range successful[1:] {
			if t.resume < minR {
				minR = t.resume
			}
			if t.resume > maxR {
				maxR = t.resume
			}
			if t.command < minC {
				minC = t.command
			}
			if t.command > maxC {
				maxC = t.command
			}
		}
		fmt.Printf("   Resume:  Min %s | Max %s\n", fmtDur(minR), fmtDur(maxR))
		fmt.Printf("   Command: Min %s | Max %s\n", fmtDur(minC), fmtDur(maxC))
	}
}

func colorForDiff(diff float64) string {
	switch {
	case diff < -5:
		return colorGreen
	case diff > 5:
		return colorRed
	default:
		return colorYellow
	}
}

func (r *runner) pauseMode(ctx context.Context, opts pauseOptions) error {
	// Benchmark mode for immediate pause
	if opts.immediate && opts.iterations > 0 {
		return r.pauseBenchmark(ctx, opts)
	}

	_, err := r.pauseOnce(ctx, opts, true)

	return err
}

func (r *runner) pauseOnce(ctx context.Context, opts pauseOptions, verbose bool) (pauseTimings, error) {
	runtime := sandbox.RuntimeMetadata{
		TemplateID:  r.buildID,
		TeamID:      "local",
		SandboxID:   fmt.Sprintf("sbx-%d", time.Now().UnixNano()),
		ExecutionID: fmt.Sprintf("exec-%d", time.Now().UnixNano()),
	}

	if verbose {
		fmt.Println("🚀 Starting sandbox...")
	}
	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	resumeDur := time.Since(t0)
	if err != nil {
		return pauseTimings{resume: resumeDur, err: err}, err
	}
	defer sbx.Close(context.WithoutCancel(ctx))

	// Register sandbox in map for TCP firewall to find
	r.sandboxes.Insert(sbx)
	defer r.sandboxes.Remove(runtime.SandboxID)

	if verbose {
		fmt.Printf("✅ Sandbox resumed in %s\n", resumeDur)
	}

	// Handle pause trigger based on options
	if opts.command != "" {
		if verbose {
			fmt.Printf("🔧 Running command: %s\n", opts.command)
		}
		if err := runCommandInSandbox(ctx, sbx, opts.command); err != nil {
			return pauseTimings{resume: resumeDur, err: err}, fmt.Errorf("command failed: %w", err)
		}
		if verbose {
			fmt.Println("✅ Command completed successfully")
		}
	} else if opts.signalName != "" {
		sig := parseSignal(opts.signalName)
		if sig == nil {
			err := fmt.Errorf("unknown signal: %s", opts.signalName)

			return pauseTimings{resume: resumeDur, err: err}, err
		}
		fmt.Printf("⏳ Waiting for %s signal...\n", opts.signalName)
		fmt.Printf("   sudo nsenter --net=/var/run/netns/%s ssh -o StrictHostKeyChecking=no root@169.254.0.21\n", sbx.Slot.NamespaceID())

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, sig)
		select {
		case <-ctx.Done():
			return pauseTimings{resume: resumeDur, err: ctx.Err()}, ctx.Err()
		case <-sigCh:
			fmt.Printf("📨 Received %s signal\n", opts.signalName)
		}
	}
	// For opts.immediate, we proceed directly to pause

	if verbose {
		fmt.Printf("⏸️  Pausing sandbox (new build: %s)...\n", opts.newBuildID)
	}

	// Sync and drop caches before pause
	if err := syncAndDropCaches(ctx, sbx); err != nil && verbose {
		fmt.Printf("⚠️  Warning: sync/drop_caches failed: %v\n", err)
	}

	// Get original metadata and update for new build
	origMeta, err := r.tmpl.Metadata()
	if err != nil {
		return pauseTimings{resume: resumeDur, err: err}, fmt.Errorf("failed to get metadata: %w", err)
	}

	newMeta := origMeta
	newMeta.Template.BuildID = opts.newBuildID

	// Pause and create snapshot
	pauseStart := time.Now()
	snapshot, err := sbx.Pause(ctx, newMeta)
	pauseDur := time.Since(pauseStart)
	totalDur := time.Since(t0)

	timings := pauseTimings{
		resume: resumeDur,
		pause:  pauseDur,
		total:  totalDur,
		err:    err,
	}

	if err != nil {
		return timings, fmt.Errorf("failed to pause: %w", err)
	}
	defer snapshot.Close(context.WithoutCancel(ctx))

	if verbose {
		fmt.Println()
		fmt.Println("📊 Timing breakdown:")
		fmt.Printf("   Resume: %s\n", fmtDur(resumeDur))
		fmt.Printf("   Pause:  %s\n", fmtDur(pauseDur))
		fmt.Printf("   Total:  %s\n", fmtDur(totalDur))
	}

	// Only upload when not in benchmark mode (verbose = true means single run)
	if verbose {
		templateFiles := storage.TemplateFiles{BuildID: opts.newBuildID}
		if opts.isRemoteStorage {
			fmt.Println("📤 Uploading snapshot...")
			if err := snapshot.UploadSingleLayer(ctx, r.storage, templateFiles); err != nil {
				return timings, fmt.Errorf("failed to upload snapshot: %w", err)
			}
			fmt.Println("✅ Snapshot uploaded successfully")
		} else {
			fmt.Println("💾 Saving snapshot to local storage...")
			if err := snapshot.UploadSingleLayer(ctx, r.storage, templateFiles); err != nil {
				return timings, fmt.Errorf("failed to save snapshot: %w", err)
			}
			fmt.Println("✅ Snapshot saved successfully")
		}

		fmt.Printf("\n✅ Build finished: %s\n", opts.newBuildID)
		printArtifactSizes(opts.storagePath, opts.newBuildID)
	}

	return timings, nil
}

func (r *runner) pauseBenchmark(ctx context.Context, opts pauseOptions) error {
	results := make([]pauseTimings, 0, opts.iterations)

	fmt.Println("📦 Benchmarking pause (resume -> snapshot)")
	fmt.Printf("   Iterations: %d, Cold start: %v\n\n", opts.iterations, r.coldStart)

	for i := range opts.iterations {
		if ctx.Err() != nil {
			break
		}

		// Clear caches for cold start
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

		// Generate unique build ID for each iteration (not saved)
		iterOpts := opts
		iterOpts.newBuildID = uuid.New().String()

		fmt.Printf("\r[%d/%d] Running...    ", i+1, opts.iterations)
		timings, _ := r.pauseOnce(ctx, iterOpts, false)
		results = append(results, timings)

		if timings.err != nil {
			fmt.Printf("\r[%d/%d] ❌ Failed: %v\n", i+1, opts.iterations, timings.err)

			break
		}
	}
	fmt.Print("\r                         \r")

	printPauseResults(results)

	// Return last error if any
	for _, t := range results {
		if t.err != nil {
			return t.err
		}
	}

	return nil
}

func printPauseResults(results []pauseTimings) {
	if len(results) == 0 {
		return
	}

	// Separate successful results
	var successful []pauseTimings
	for _, r := range results {
		if r.err == nil {
			successful = append(successful, r)
		}
	}

	if len(successful) == 0 {
		fmt.Println("\n❌ All runs failed")

		return
	}

	// Calculate averages
	var totalResume, totalPause, totalTotal time.Duration
	for _, t := range successful {
		totalResume += t.resume
		totalPause += t.pause
		totalTotal += t.total
	}
	n := len(successful)
	avgResume := totalResume / time.Duration(n)
	avgPause := totalPause / time.Duration(n)
	avgTotal := totalTotal / time.Duration(n)

	// Print individual results
	fmt.Println("\n📋 Run times (resume / pause / total):")
	for i, t := range results {
		if t.err != nil {
			fmt.Printf("   [%2d] ❌ Failed: %v\n", i+1, t.err)

			continue
		}

		resumeDiff := float64(t.resume-avgResume) / float64(avgResume) * 100
		pauseDiff := float64(t.pause-avgPause) / float64(avgPause) * 100

		fmt.Printf("   [%2d] %s / %s / %s  (resume: %s%+.1f%%%s, pause: %s%+.1f%%%s)\n",
			i+1,
			fmtDur(t.resume), fmtDur(t.pause), fmtDur(t.total),
			colorForDiff(resumeDiff), resumeDiff, colorReset,
			colorForDiff(pauseDiff), pauseDiff, colorReset)
	}

	// Print summary
	fmt.Printf("\n📊 Summary (%d runs):\n", n)
	fmt.Printf("   Resume: Avg %s\n", fmtDur(avgResume))
	fmt.Printf("   Pause:  Avg %s\n", fmtDur(avgPause))
	fmt.Printf("   Total:  Avg %s\n", fmtDur(avgTotal))

	// Min/Max for each
	if n > 1 {
		minR, maxR := successful[0].resume, successful[0].resume
		minP, maxP := successful[0].pause, successful[0].pause
		for _, t := range successful[1:] {
			if t.resume < minR {
				minR = t.resume
			}
			if t.resume > maxR {
				maxR = t.resume
			}
			if t.pause < minP {
				minP = t.pause
			}
			if t.pause > maxP {
				maxP = t.pause
			}
		}
		fmt.Printf("   Resume: Min %s | Max %s\n", fmtDur(minR), fmtDur(maxR))
		fmt.Printf("   Pause:  Min %s | Max %s\n", fmtDur(minP), fmtDur(maxP))
	}
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

func run(ctx context.Context, buildID string, iterations int, coldStart, noPrefetch, verbose bool, pauseOpts pauseOptions, runOpts runOptions) error {
	// Always suppress OTEL tracing logs
	cmdutil.SuppressOTELLogs()

	// Silence other loggers unless verbose mode
	var l logger.Logger
	if !verbose {
		cmdutil.SuppressNoisyLogs()
		l = logger.NewNopLogger()
	} else {
		l, _ = logger.NewDevelopmentLogger()
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
	logLevel := ldlog.Error
	if verbose {
		logLevel = ldlog.Info
	}
	flags, _ := featureflags.NewClientWithLogLevel(logLevel)

	if verbose {
		fmt.Println("🔧 Creating storage provider...")
	}
	persistence, err := storage.GetTemplateStorageProvider(ctx, nil)
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
	sandboxes := sandbox.NewSandboxesMap()
	factory := sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, flags)

	if verbose {
		fmt.Println("🔧 Starting TCP firewall...")
	}
	tcpFw := tcpfirewall.New(l, config.NetworkConfig, sandboxes, noop.NewMeterProvider(), flags)
	go tcpFw.Start(ctx)
	defer tcpFw.Close(context.WithoutCancel(ctx))

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
		sandboxes:  sandboxes,
		tmpl:       tmpl,
		buildID:    buildID,
		cache:      cache,
		coldStart:  coldStart,
		noPrefetch: noPrefetch,
		config:     config.BuilderConfig,
		storage:    persistence,
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

	if runOpts.enabled() {
		return r.cmdMode(ctx, runOpts)
	}

	if pauseOpts.enabled() {
		return r.pauseMode(ctx, pauseOpts)
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
			blockSize := memfile.BlockSize()
			fmt.Printf("   Memfile: %d MB (%d KB blocks)\n", size>>20, blockSize>>10)
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

// runCommandInSandbox runs a command inside the sandbox via envd
func runCommandInSandbox(ctx context.Context, sbx *sandbox.Sandbox, command string) error {
	// Connect directly to envd on the sandbox
	envdURL := fmt.Sprintf("http://%s:%d", sbx.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	hc := http.Client{
		Timeout:   10 * time.Minute,
		Transport: sandbox.SandboxHttpTransport,
	}

	processC := processconnect.NewProcessClient(&hc, envdURL)

	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "/bin/bash",
			Args: []string{"-l", "-c", command},
		},
	})
	grpc.SetUserHeader(req.Header(), "root")

	// Set access token if available
	if sbx.Config.Envd.AccessToken != nil {
		req.Header().Set("X-Access-Token", *sbx.Config.Envd.AccessToken)
	}

	stream, err := processC.Start(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}
	defer stream.Close()

	// Read stream until completion
	for stream.Receive() {
		msg := stream.Msg()
		event := msg.GetEvent()
		if event == nil {
			continue
		}

		switch e := event.GetEvent().(type) {
		case *process.ProcessEvent_Data:
			// Handle data events (stdout/stderr)
			if data := e.Data; data != nil {
				if stdout := data.GetStdout(); stdout != nil {
					fmt.Print(string(stdout))
				}
				if stderrData := data.GetStderr(); stderrData != nil {
					fmt.Print(string(stderrData))
				}
			}
		case *process.ProcessEvent_End:
			// Handle exit event
			if end := e.End; end != nil {
				if !end.GetExited() || end.GetExitCode() != 0 {
					return fmt.Errorf("command exited with code %d", end.GetExitCode())
				}

				return nil
			}
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("stream error: %w", err)
	}

	return nil
}

// syncAndDropCaches syncs filesystem and drops caches before snapshot
func syncAndDropCaches(ctx context.Context, sbx *sandbox.Sandbox) error {
	// Run sync command
	if err := runCommandInSandbox(ctx, sbx, "/usr/bin/busybox sync"); err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	// Drop caches
	if err := runCommandInSandbox(ctx, sbx, "echo 3 > /proc/sys/vm/drop_caches"); err != nil {
		return fmt.Errorf("drop_caches failed: %w", err)
	}

	return nil
}

// parseSignal converts a signal name to os.Signal
func parseSignal(name string) os.Signal {
	name = strings.ToUpper(strings.TrimPrefix(name, "SIG"))
	signals := map[string]os.Signal{
		"TERM":  syscall.SIGTERM,
		"USR1":  syscall.SIGUSR1,
		"USR2":  syscall.SIGUSR2,
		"HUP":   syscall.SIGHUP,
		"INT":   syscall.SIGINT,
		"QUIT":  syscall.SIGQUIT,
		"CONT":  syscall.SIGCONT,
		"WINCH": syscall.SIGWINCH,
	}

	return signals[name]
}

// printArtifactSizes prints artifact sizes
func printArtifactSizes(_, buildID string) {
	basePath := os.Getenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH")
	if basePath == "" {
		return
	}

	dir := filepath.Join(basePath, buildID)

	fmt.Println("\n📦 Artifacts:")

	for _, a := range cmdutil.MainArtifacts() {
		path := filepath.Join(dir, a.File)
		_, actual, err := cmdutil.GetFileSizes(path)
		if err != nil {
			continue
		}

		headerPath := filepath.Join(dir, a.HeaderFile)
		totalSize, blockSize := cmdutil.GetHeaderInfo(headerPath)
		if totalSize == 0 {
			fmt.Printf("   %s: %d MB (this layer)\n", a.Name, actual>>20)

			continue
		}

		pct := float64(actual) / float64(totalSize) * 100
		fmt.Printf("   %s: %d MB diff / %d MB total (%.1f%%), block size: %d KB\n",
			a.Name, actual>>20, totalSize>>20, pct, blockSize>>10)
	}

	for _, a := range cmdutil.SmallArtifacts() {
		path := filepath.Join(dir, a.File)
		if actual, err := cmdutil.GetActualFileSize(path); err == nil {
			fmt.Printf("   %s: %d KB\n", a.Name, actual>>10)
		}
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
	fmt.Println("\n📋 Run times:")
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
