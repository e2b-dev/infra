//go:build linux

// Real Firecracker memory sharing benchmark.
//
// This benchmark keeps resumed sandboxes alive long enough to sample host
// smaps_rollup, Firecracker resident/dirty bitmaps, and optional checkpoint
// size. It intentionally does not run by default in CI; it needs the same
// privileged host setup as BenchmarkRealFirecracker.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/cgroup"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const memorySharingBenchmarkName = "real-firecracker-memory-sharing"

type memoryBenchmarkSettings struct {
	OutputDir            string
	SampleDelay          time.Duration
	IncludeCheckpoint    bool
	IncludePauseSnapshot bool
}

type memoryRunOptions struct {
	settings        memoryBenchmarkSettings
	concurrency     int
	iteration       int
	templateMeta    metadata.Template
	checkpointPath  string
	sharedMemfiles  *memory.SharedMemfileManager
}

type memoryResumeResult struct {
	sandboxID string
	latency   time.Duration
	err       error
}

type smapsRollup struct {
	RSSBytes             int64 `json:"rss_bytes"`
	PSSBytes             int64 `json:"pss_bytes"`
	SharedCleanBytes     int64 `json:"shared_clean_bytes"`
	SharedDirtyBytes     int64 `json:"shared_dirty_bytes"`
	PrivateCleanBytes    int64 `json:"private_clean_bytes"`
	PrivateDirtyBytes    int64 `json:"private_dirty_bytes"`
	ReferencedBytes      int64 `json:"referenced_bytes"`
	AnonymousBytes       int64 `json:"anonymous_bytes"`
	LazyFreeBytes        int64 `json:"lazy_free_bytes"`
	AnonHugePagesBytes   int64 `json:"anon_huge_pages_bytes"`
	SharedHugetlbBytes   int64 `json:"shared_hugetlb_bytes"`
	PrivateHugetlbBytes  int64 `json:"private_hugetlb_bytes"`
	SwapBytes            int64 `json:"swap_bytes"`
	SwapPSSBytes         int64 `json:"swap_pss_bytes"`
	LockedBytes          int64 `json:"locked_bytes"`
}

func (s smapsRollup) SharedRSSBytes() int64 {
	return s.SharedCleanBytes + s.SharedDirtyBytes + s.SharedHugetlbBytes
}

func (s smapsRollup) PrivateRSSBytes() int64 {
	return s.PrivateCleanBytes + s.PrivateDirtyBytes + s.PrivateHugetlbBytes
}

func (s smapsRollup) DirtyRSSBytes() int64 {
	return s.SharedDirtyBytes + s.PrivateDirtyBytes
}

func (s smapsRollup) DirtyRSSRatio() float64 {
	if s.RSSBytes == 0 {
		return 0
	}

	return float64(s.DirtyRSSBytes()) / float64(s.RSSBytes)
}

type fileSizeStats struct {
	Path           string `json:"path"`
	Exists         bool   `json:"exists"`
	LogicalBytes   int64  `json:"logical_bytes"`
	AllocatedBytes int64  `json:"allocated_bytes"`
}

type checkpointStats struct {
	BasePath            string        `json:"base_path"`
	Data                fileSizeStats `json:"data"`
	Bitmap              fileSizeStats `json:"bitmap"`
	TotalLogicalBytes   int64         `json:"total_logical_bytes"`
	TotalAllocatedBytes int64         `json:"total_allocated_bytes"`
}

type pauseSnapshotStats struct {
	Snapfile              fileSizeStats `json:"snapfile"`
	MemoryDiffLogical    int64         `json:"memory_diff_logical_bytes"`
	MemoryDiffFile       int64         `json:"memory_diff_file_bytes"`
	RootfsDiffLogical    int64         `json:"rootfs_diff_logical_bytes"`
	RootfsDiffFile       int64         `json:"rootfs_diff_file_bytes"`
	TotalLogicalBytes    int64         `json:"total_logical_bytes"`
	TotalLocalFileBytes  int64         `json:"total_local_file_bytes"`
	MemoryDiffBlockBytes int64         `json:"memory_diff_block_bytes"`
	RootfsDiffBlockBytes int64         `json:"rootfs_diff_block_bytes"`
}

type memorySample struct {
	Timestamp time.Time `json:"timestamp"`
	Benchmark string    `json:"benchmark"`

	Concurrency int `json:"concurrency"`
	Iteration   int `json:"iteration"`

	TemplateID string `json:"template_id"`
	BuildID    string `json:"build_id"`
	SandboxID  string `json:"sandbox_id"`

	LatencyMs   int64 `json:"latency_ms"`
	WallClockMs int64 `json:"wall_clock_ms"`

	ProcessPID int `json:"process_pid,omitempty"`
	SmapsPID   int `json:"smaps_pid,omitempty"`

	Smaps smapsRollup `json:"smaps"`

	SharedRSSBytes      int64   `json:"shared_rss_bytes"`
	PrivateRSSBytes     int64   `json:"private_rss_bytes"`
	HostDirtyRSSBytes   int64   `json:"host_dirty_rss_bytes"`
	HostDirtyRSSRatio   float64 `json:"host_dirty_rss_ratio"`
	GuestMemoryBytes    int64   `json:"guest_memory_bytes"`
	GuestPageCount      uint64  `json:"guest_page_count"`
	GuestResidentPages  uint64  `json:"guest_resident_pages"`
	GuestResidentBytes  uint64  `json:"guest_resident_bytes"`
	GuestDirtyPages     uint64  `json:"guest_dirty_pages"`
	GuestDirtyBytes     uint64  `json:"guest_dirty_bytes"`
	GuestDirtyRatio     float64 `json:"guest_dirty_ratio"`
	EmptyResidentPages  uint64  `json:"empty_resident_pages"`
	EmptyResidentBytes  uint64  `json:"empty_resident_bytes"`
	PageSizeBytes       int64   `json:"page_size_bytes"`
	SharedMemfileCount  int     `json:"shared_memfile_count"`
	SharedMemfileBytes  int64   `json:"shared_memfile_bytes"`
	CheckpointPath      string  `json:"checkpoint_path,omitempty"`
	DiagnosticError     string  `json:"diagnostic_error,omitempty"`
	CloseError          string  `json:"close_error,omitempty"`
	PauseSnapshotError  string  `json:"pause_snapshot_error,omitempty"`
	CheckpointStatError string  `json:"checkpoint_stat_error,omitempty"`
	Error               string  `json:"error,omitempty"`

	Checkpoint   *checkpointStats   `json:"checkpoint,omitempty"`
	PauseSnapshot *pauseSnapshotStats `json:"pause_snapshot,omitempty"`
}

type memoryAggregate struct {
	samples []memorySample
}

func (a *memoryAggregate) Add(samples []memorySample) {
	a.samples = append(a.samples, samples...)
}

func (a *memoryAggregate) successfulSamples() []memorySample {
	successful := make([]memorySample, 0, len(a.samples))
	for _, sample := range a.samples {
		if sample.Error == "" {
			successful = append(successful, sample)
		}
	}

	return successful
}

func BenchmarkRealFirecrackerMemorySharing(b *testing.B) {
	if os.Geteuid() != 0 {
		b.Skip("skipping benchmark because not running as root")
	}

	settings := parseMemoryBenchmarkSettings(b)
	output, err := newMemoryBenchmarkOutput(settings.OutputDir)
	require.NoError(b, err)
	b.Cleanup(func() {
		assert.NoError(b, output.Close())
	})
	b.Logf("memory benchmark output: %s", settings.OutputDir)

	levels := parseRealConcurrencyLevels()

	const (
		kernelVersion = "vmlinux-6.1.102"
		fcVersion     = "v1.12.1_210cbac"
		templateID    = "fcb33d09-3141-42c4-8d3b-c2df411681db"
		buildID       = "ba6aae36-74f7-487a-b6f7-74fd7c94e479"
		useHugePages  = false
	)

	persistenceDir := getPersistenceDir()
	kernelsDir := filepath.Join(persistenceDir, "kernels")
	require.NoError(b, os.MkdirAll(kernelsDir, 0o755))

	tempDir := b.TempDir()
	abs := func(s string) string {
		return utils.Must(filepath.Abs(s))
	}

	linuxKernelURL, err := url.JoinPath("https://storage.googleapis.com/e2b-prod-public-builds/kernels/", kernelVersion, "vmlinux.bin")
	require.NoError(b, err)
	linuxKernelFilename := filepath.Join(kernelsDir, kernelVersion, "vmlinux.bin")
	downloadKernel(b, linuxKernelFilename, linuxKernelURL)

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

	sharedMemfiles := memory.NewSharedMemfileManager()
	b.Cleanup(func() {
		assert.NoError(b, sharedMemfiles.Close())
	})

	sandboxes := sandbox.NewSandboxesMap()
	sandboxFactory := sandbox.NewFactory(
		config.BuilderConfig, networkPool, devicePool,
		featureFlags, hoststats.NewNoopDelivery(), cgroupManager, network.NewNoopEgressProxy(), sandboxes,
		sharedMemfiles,
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

	tmpl, err := templateCache.GetTemplate(b.Context(), buildID, false, false)
	require.NoError(b, err, "template not found in cache - was it built previously?")

	templateMeta, err := tmpl.Metadata()
	require.NoError(b, err)

	checkpointPath := checkpointPathForTemplate(tmpl)
	if settings.IncludeCheckpoint && checkpointPath == "" {
		b.Log("checkpoint stats requested, but template is not layered or has no shared layer")
	}

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

	for _, n := range levels {
		b.Run(fmt.Sprintf("concurrency-%d", n), func(b *testing.B) {
			aggregate := &memoryAggregate{}
			iteration := 0

			for b.Loop() {
				iteration++
				samples, wall := runMemoryConcurrentResume(
					b,
					sandboxFactory,
					tmpl,
					sandboxConfig,
					templateID,
					buildID,
					memoryRunOptions{
						settings:       settings,
						concurrency:    n,
						iteration:      iteration,
						templateMeta:   templateMeta,
						checkpointPath: checkpointPath,
						sharedMemfiles: sharedMemfiles,
					},
				)

				for i := range samples {
					samples[i].WallClockMs = wall.Milliseconds()
					require.NoError(b, output.WriteSample(samples[i]))
				}

				aggregate.Add(samples)
			}

			reportMemoryAggregate(b, n, aggregate, output)
		})
	}
}

func parseMemoryBenchmarkSettings(b *testing.B) memoryBenchmarkSettings {
	b.Helper()

	outputDir := os.Getenv("MEMORY_BENCH_OUTPUT_DIR")
	if outputDir == "" {
		outputDir = filepath.Join("results", "memory-sharing-"+time.Now().UTC().Format("20060102T150405Z"))
	}
	outputDir = utils.Must(filepath.Abs(outputDir))

	return memoryBenchmarkSettings{
		OutputDir:            outputDir,
		SampleDelay:          parseDurationEnv(b, "MEMORY_BENCH_SAMPLE_DELAY", 0),
		IncludeCheckpoint:    parseBoolEnv("MEMORY_BENCH_CHECKPOINT", false),
		IncludePauseSnapshot: parseBoolEnv("MEMORY_BENCH_PAUSE_CHECKPOINT", false),
	}
}

func parseDurationEnv(b *testing.B, name string, fallback time.Duration) time.Duration {
	b.Helper()

	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		b.Fatalf("invalid %s duration %q: %v", name, raw, err)
	}

	return d
}

func parseBoolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}

	return value
}

func runMemoryConcurrentResume(
	b *testing.B,
	factory *sandbox.Factory,
	tmpl template.Template,
	config *sandbox.Config,
	templateID string,
	buildID string,
	opts memoryRunOptions,
) ([]memorySample, time.Duration) {
	b.Helper()

	n := opts.concurrency
	results := make([]memoryResumeResult, n)
	created := make([]*sandbox.Sandbox, n)

	gate := make(chan struct{})
	var wg sync.WaitGroup

	b.StopTimer()

	for i := range n {
		wg.Go(func() {
			runtime := sandbox.RuntimeMetadata{
				TemplateID:  templateID,
				SandboxID:   fmt.Sprintf("mem-bench-%d-%s", i, uuid.NewString()[:8]),
				ExecutionID: fmt.Sprintf("mem-bench-exec-%d", i),
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

			results[i] = memoryResumeResult{
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

	if opts.settings.SampleDelay > 0 {
		time.Sleep(opts.settings.SampleDelay)
	}

	samples := make([]memorySample, 0, n)
	for i, result := range results {
		sample := memorySample{
			Timestamp:   time.Now().UTC(),
			Benchmark:   memorySharingBenchmarkName,
			Concurrency: n,
			Iteration:   opts.iteration,
			TemplateID:  templateID,
			BuildID:     buildID,
			SandboxID:   result.sandboxID,
			LatencyMs:   result.latency.Milliseconds(),
		}

		sbx := created[i]
		if result.err != nil {
			sample.Error = result.err.Error()
			samples = append(samples, sample)
			continue
		}
		if sbx == nil {
			sample.Error = "sandbox resume returned nil sandbox without error"
			samples = append(samples, sample)
			continue
		}

		collectLiveMemorySample(b.Context(), sbx, opts, &sample)

		if opts.settings.IncludePauseSnapshot {
			collectPauseSnapshotSample(b.Context(), sbx, opts.templateMeta, &sample)
		}

		if closeErr := sbx.Close(b.Context()); closeErr != nil {
			sample.CloseError = closeErr.Error()
		}

		if opts.settings.IncludeCheckpoint && opts.checkpointPath != "" {
			sample.CheckpointPath = opts.checkpointPath
			checkpoint, statErr := collectCheckpointStats(opts.checkpointPath)
			if statErr != nil {
				sample.CheckpointStatError = statErr.Error()
			} else {
				sample.Checkpoint = checkpoint
			}
		}

		samples = append(samples, sample)
	}

	b.StartTimer()

	return samples, wallDuration
}

func collectLiveMemorySample(ctx context.Context, sbx *sandbox.Sandbox, opts memoryRunOptions, sample *memorySample) {
	metrics, metricsErr := sbx.RuntimeMemoryMetrics(ctx)
	if metricsErr != nil {
		sample.DiagnosticError = appendErrorText(sample.DiagnosticError, metricsErr)
	}

	sample.ProcessPID = metrics.ProcessPID
	sample.PageSizeBytes = metrics.PageSizeBytes
	sample.GuestMemoryBytes = metrics.GuestMemoryBytes
	sample.GuestPageCount = metrics.GuestPageCount
	sample.GuestResidentPages = metrics.ResidentPages
	sample.GuestResidentBytes = metrics.ResidentBytes
	sample.EmptyResidentPages = metrics.EmptyResidentPages
	sample.EmptyResidentBytes = metrics.EmptyResidentBytes
	sample.GuestDirtyPages = metrics.DirtyPages
	sample.GuestDirtyBytes = metrics.DirtyBytes
	sample.GuestDirtyRatio = metrics.DirtyRatio

	if metrics.ProcessPID > 0 {
		smapsPID, resolveErr := resolveSmapsPID(metrics.ProcessPID)
		if resolveErr != nil {
			sample.DiagnosticError = appendErrorText(sample.DiagnosticError, resolveErr)
			smapsPID = metrics.ProcessPID
		}
		sample.SmapsPID = smapsPID

		smaps, smapsErr := readSmapsRollup(smapsPID)
		if smapsErr != nil {
			sample.DiagnosticError = appendErrorText(sample.DiagnosticError, smapsErr)
		} else {
			sample.Smaps = smaps
			sample.SharedRSSBytes = smaps.SharedRSSBytes()
			sample.PrivateRSSBytes = smaps.PrivateRSSBytes()
			sample.HostDirtyRSSBytes = smaps.DirtyRSSBytes()
			sample.HostDirtyRSSRatio = smaps.DirtyRSSRatio()
		}
	}

	if opts.sharedMemfiles != nil {
		count, bytes := opts.sharedMemfiles.GetStats()
		sample.SharedMemfileCount = count
		sample.SharedMemfileBytes = bytes
	}
}

func collectPauseSnapshotSample(ctx context.Context, sbx *sandbox.Sandbox, templateMeta metadata.Template, sample *memorySample) {
	snap, err := sbx.Pause(ctx, templateMeta, sandbox.SnapshotUseCasePause)
	if err != nil {
		sample.PauseSnapshotError = err.Error()
		return
	}
	defer func() {
		if closeErr := snap.Close(ctx); closeErr != nil {
			sample.PauseSnapshotError = appendErrorText(sample.PauseSnapshotError, closeErr)
		}
	}()

	stats := &pauseSnapshotStats{}

	if snap.Snapfile != nil {
		fileStats, statErr := statFileSize(snap.Snapfile.Path())
		if statErr != nil {
			sample.PauseSnapshotError = appendErrorText(sample.PauseSnapshotError, statErr)
		} else {
			stats.Snapfile = fileStats
		}
	}

	memLogical, memFile, memErr := diffSizes(ctx, snap.MemorySnapshot.Diff)
	if memErr != nil {
		sample.PauseSnapshotError = appendErrorText(sample.PauseSnapshotError, fmt.Errorf("memory diff size: %w", memErr))
	}
	rootLogical, rootFile, rootErr := diffSizes(ctx, snap.RootfsDiff)
	if rootErr != nil {
		sample.PauseSnapshotError = appendErrorText(sample.PauseSnapshotError, fmt.Errorf("rootfs diff size: %w", rootErr))
	}

	stats.MemoryDiffLogical = memLogical
	stats.MemoryDiffFile = memFile
	stats.RootfsDiffLogical = rootLogical
	stats.RootfsDiffFile = rootFile
	stats.MemoryDiffBlockBytes = snap.MemorySnapshot.Diff.BlockSize()
	stats.RootfsDiffBlockBytes = snap.RootfsDiff.BlockSize()
	stats.TotalLogicalBytes = stats.Snapfile.LogicalBytes + memLogical + rootLogical
	stats.TotalLocalFileBytes = stats.Snapfile.AllocatedBytes + memFile + rootFile
	sample.PauseSnapshot = stats
}

func diffSizes(ctx context.Context, d build.Diff) (logical int64, file int64, err error) {
	logical, err = d.Size(ctx)
	if err != nil && !isNoDiff(err) {
		return 0, 0, err
	}
	if isNoDiff(err) {
		err = nil
	}

	file, fileErr := d.FileSize(ctx)
	if fileErr != nil && !isNoDiff(fileErr) {
		return logical, 0, fileErr
	}

	return logical, file, nil
}

func isNoDiff(err error) bool {
	var noDiff build.NoDiffError
	return errors.As(err, &noDiff)
}

func appendErrorText(existing string, err error) string {
	if err == nil {
		return existing
	}
	if existing == "" {
		return err.Error()
	}

	return existing + "; " + err.Error()
}

func reportMemoryAggregate(b *testing.B, concurrency int, aggregate *memoryAggregate, output *memoryBenchmarkOutput) {
	b.Helper()

	successful := aggregate.successfulSamples()
	failures := len(aggregate.samples) - len(successful)
	b.ReportMetric(float64(len(successful)), "ok")
	b.ReportMetric(float64(failures), "fail")

	if len(successful) == 0 {
		require.NoError(b, output.WriteSummary(memorySummary{
			Concurrency: concurrency,
			Samples:     len(aggregate.samples),
			Failures:    failures,
		}))
		return
	}

	latencies := make([]time.Duration, 0, len(successful))
	var totalLatencyMs int64
	var totalSharedRSS int64
	var totalPrivateRSS int64
	var totalHostDirtyRatio float64
	var totalGuestDirtyRatio float64
	var totalGuestDirtyBytes uint64
	var totalCheckpointAllocated int64
	var checkpointSamples int

	for _, sample := range successful {
		latency := time.Duration(sample.LatencyMs) * time.Millisecond
		latencies = append(latencies, latency)
		totalLatencyMs += sample.LatencyMs
		totalSharedRSS += sample.SharedRSSBytes
		totalPrivateRSS += sample.PrivateRSSBytes
		totalHostDirtyRatio += sample.HostDirtyRSSRatio
		totalGuestDirtyRatio += sample.GuestDirtyRatio
		totalGuestDirtyBytes += sample.GuestDirtyBytes
		if sample.Checkpoint != nil {
			checkpointSamples++
			totalCheckpointAllocated += sample.Checkpoint.TotalAllocatedBytes
		}
	}
	slices.Sort(latencies)

	count := float64(len(successful))
	avgLatencyMs := float64(totalLatencyMs) / count
	avgSharedRSSMB := bytesToMiB(float64(totalSharedRSS) / count)
	avgPrivateRSSMB := bytesToMiB(float64(totalPrivateRSS) / count)
	avgHostDirtyRatio := totalHostDirtyRatio / count
	avgGuestDirtyRatio := totalGuestDirtyRatio / count
	avgGuestDirtyMB := bytesToMiB(float64(totalGuestDirtyBytes) / count)

	b.ReportMetric(avgSharedRSSMB, "avg-shared-rss-mb")
	b.ReportMetric(avgPrivateRSSMB, "avg-private-rss-mb")
	b.ReportMetric(avgHostDirtyRatio, "avg-host-dirty-ratio")
	b.ReportMetric(avgGuestDirtyRatio, "avg-guest-dirty-ratio")
	b.ReportMetric(avgGuestDirtyMB, "avg-guest-dirty-mb")

	var avgCheckpointAllocatedMB float64
	if checkpointSamples > 0 {
		avgCheckpointAllocatedMB = bytesToMiB(float64(totalCheckpointAllocated) / float64(checkpointSamples))
		b.ReportMetric(avgCheckpointAllocatedMB, "avg-checkpoint-allocated-mb")
	}

	summary := memorySummary{
		Concurrency:                  concurrency,
		Samples:                      len(aggregate.samples),
		Failures:                     failures,
		AvgLatencyMs:                 avgLatencyMs,
		P50LatencyMs:                 float64(percentile(latencies, 50).Milliseconds()),
		P95LatencyMs:                 float64(percentile(latencies, 95).Milliseconds()),
		AvgSharedRSSMB:               avgSharedRSSMB,
		AvgPrivateRSSMB:              avgPrivateRSSMB,
		AvgHostDirtyRatio:            avgHostDirtyRatio,
		AvgGuestDirtyRatio:           avgGuestDirtyRatio,
		AvgGuestDirtyMB:              avgGuestDirtyMB,
		CheckpointSamples:            checkpointSamples,
		AvgCheckpointAllocatedMB:     avgCheckpointAllocatedMB,
	}
	require.NoError(b, output.WriteSummary(summary))
}

func bytesToMiB(bytes float64) float64 {
	return bytes / 1024 / 1024
}

func parseSmapsRollup(data []byte) (smapsRollup, error) {
	var rollup smapsRollup

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || !strings.HasSuffix(fields[0], ":") {
			continue
		}

		key := strings.TrimSuffix(fields[0], ":")
		valueKiB, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return smapsRollup{}, fmt.Errorf("parse smaps_rollup %s value %q: %w", key, fields[1], err)
		}
		valueBytes := valueKiB * 1024

		switch key {
		case "Rss":
			rollup.RSSBytes = valueBytes
		case "Pss":
			rollup.PSSBytes = valueBytes
		case "Shared_Clean":
			rollup.SharedCleanBytes = valueBytes
		case "Shared_Dirty":
			rollup.SharedDirtyBytes = valueBytes
		case "Private_Clean":
			rollup.PrivateCleanBytes = valueBytes
		case "Private_Dirty":
			rollup.PrivateDirtyBytes = valueBytes
		case "Referenced":
			rollup.ReferencedBytes = valueBytes
		case "Anonymous":
			rollup.AnonymousBytes = valueBytes
		case "LazyFree":
			rollup.LazyFreeBytes = valueBytes
		case "AnonHugePages":
			rollup.AnonHugePagesBytes = valueBytes
		case "Shared_Hugetlb":
			rollup.SharedHugetlbBytes = valueBytes
		case "Private_Hugetlb":
			rollup.PrivateHugetlbBytes = valueBytes
		case "Swap":
			rollup.SwapBytes = valueBytes
		case "SwapPss":
			rollup.SwapPSSBytes = valueBytes
		case "Locked":
			rollup.LockedBytes = valueBytes
		}
	}
	if err := scanner.Err(); err != nil {
		return smapsRollup{}, fmt.Errorf("scan smaps_rollup: %w", err)
	}

	return rollup, nil
}

func readSmapsRollup(pid int) (smapsRollup, error) {
	path := filepath.Join("/proc", strconv.Itoa(pid), "smaps_rollup")
	data, err := os.ReadFile(path)
	if err != nil {
		return smapsRollup{}, fmt.Errorf("read %s: %w", path, err)
	}

	return parseSmapsRollup(data)
}

func resolveSmapsPID(rootPID int) (int, error) {
	pids := collectProcessTreePIDs(rootPID)

	var bestPID int
	var bestRSS int64 = -1
	var readErr error

	for _, pid := range pids {
		if isFirecrackerProcess(pid) {
			return pid, nil
		}

		smaps, err := readSmapsRollup(pid)
		if err != nil {
			readErr = errors.Join(readErr, err)
			continue
		}
		if smaps.RSSBytes > bestRSS {
			bestRSS = smaps.RSSBytes
			bestPID = pid
		}
	}

	if bestPID > 0 {
		return bestPID, nil
	}

	return rootPID, fmt.Errorf("resolve smaps pid from %d: %w", rootPID, readErr)
}

func collectProcessTreePIDs(rootPID int) []int {
	seen := map[int]struct{}{rootPID: {}}
	queue := []int{rootPID}
	var pids []int

	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		pids = append(pids, pid)

		children, err := processChildren(pid)
		if err != nil {
			continue
		}
		for _, child := range children {
			if _, ok := seen[child]; ok {
				continue
			}
			seen[child] = struct{}{}
			queue = append(queue, child)
		}
	}

	return pids
}

func processChildren(pid int) ([]int, error) {
	path := filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(string(data))
	children := make([]int, 0, len(fields))
	for _, field := range fields {
		child, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		children = append(children, child)
	}

	return children, nil
}

func isFirecrackerProcess(pid int) bool {
	comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err == nil && strings.TrimSpace(string(comm)) == "firecracker" {
		return true
	}

	exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err == nil && filepath.Base(exe) == "firecracker" {
		return true
	}

	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(cmdline) == 0 {
		return false
	}
	firstArg := string(bytes.Split(cmdline, []byte{0})[0])

	return filepath.Base(firstArg) == "firecracker"
}

func statFileSize(path string) (fileSizeStats, error) {
	stats := fileSizeStats{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return stats, nil
		}
		return stats, fmt.Errorf("stat %s: %w", path, err)
	}

	stats.Exists = true
	stats.LogicalBytes = info.Size()
	stats.AllocatedBytes = info.Size()
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		stats.AllocatedBytes = sys.Blocks * 512
	}

	return stats, nil
}

func collectCheckpointStats(basePath string) (*checkpointStats, error) {
	data, dataErr := statFileSize(basePath + ".data")
	bitmap, bitmapErr := statFileSize(basePath + ".bitmap")

	stats := &checkpointStats{
		BasePath: basePath,
		Data:     data,
		Bitmap:   bitmap,
	}
	stats.TotalLogicalBytes = data.LogicalBytes + bitmap.LogicalBytes
	stats.TotalAllocatedBytes = data.AllocatedBytes + bitmap.AllocatedBytes

	return stats, errors.Join(dataErr, bitmapErr)
}

func checkpointPathForTemplate(tmpl template.Template) string {
	layered, ok := tmpl.(*template.LayeredTemplate)
	if !ok || layered == nil {
		return ""
	}

	layerTmpl := layered.L1()
	if layerTmpl == nil {
		layerTmpl = layered.L0()
	}
	if layerTmpl == nil {
		return ""
	}

	return layerTmpl.Files().CacheSnapfile() + ".l2_checkpoint"
}

type memoryBenchmarkOutput struct {
	mu          sync.Mutex
	samplesFile *os.File
	summaryFile *os.File
	summaryCSV  *csv.Writer
}

type memorySummary struct {
	Concurrency              int
	Samples                  int
	Failures                 int
	AvgLatencyMs             float64
	P50LatencyMs             float64
	P95LatencyMs             float64
	AvgSharedRSSMB           float64
	AvgPrivateRSSMB          float64
	AvgHostDirtyRatio        float64
	AvgGuestDirtyRatio       float64
	AvgGuestDirtyMB          float64
	CheckpointSamples        int
	AvgCheckpointAllocatedMB float64
}

func newMemoryBenchmarkOutput(outputDir string) (*memoryBenchmarkOutput, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	samplesFile, err := os.Create(filepath.Join(outputDir, "samples.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("create samples.jsonl: %w", err)
	}

	summaryFile, err := os.Create(filepath.Join(outputDir, "summary.csv"))
	if err != nil {
		_ = samplesFile.Close()
		return nil, fmt.Errorf("create summary.csv: %w", err)
	}

	out := &memoryBenchmarkOutput{
		samplesFile: samplesFile,
		summaryFile: summaryFile,
		summaryCSV:  csv.NewWriter(summaryFile),
	}

	if err := out.summaryCSV.Write([]string{
		"concurrency",
		"samples",
		"failures",
		"avg_latency_ms",
		"p50_latency_ms",
		"p95_latency_ms",
		"avg_shared_rss_mb",
		"avg_private_rss_mb",
		"avg_host_dirty_ratio",
		"avg_guest_dirty_ratio",
		"avg_guest_dirty_mb",
		"checkpoint_samples",
		"avg_checkpoint_allocated_mb",
	}); err != nil {
		_ = out.Close()
		return nil, fmt.Errorf("write summary header: %w", err)
	}
	out.summaryCSV.Flush()
	if err := out.summaryCSV.Error(); err != nil {
		_ = out.Close()
		return nil, fmt.Errorf("flush summary header: %w", err)
	}

	return out, nil
}

func (o *memoryBenchmarkOutput) WriteSample(sample memorySample) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	data, err := json.Marshal(sample)
	if err != nil {
		return fmt.Errorf("marshal memory sample: %w", err)
	}
	if _, err := o.samplesFile.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write memory sample: %w", err)
	}

	return nil
}

func (o *memoryBenchmarkOutput) WriteSummary(summary memorySummary) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	err := o.summaryCSV.Write([]string{
		strconv.Itoa(summary.Concurrency),
		strconv.Itoa(summary.Samples),
		strconv.Itoa(summary.Failures),
		formatFloat(summary.AvgLatencyMs),
		formatFloat(summary.P50LatencyMs),
		formatFloat(summary.P95LatencyMs),
		formatFloat(summary.AvgSharedRSSMB),
		formatFloat(summary.AvgPrivateRSSMB),
		formatFloat(summary.AvgHostDirtyRatio),
		formatFloat(summary.AvgGuestDirtyRatio),
		formatFloat(summary.AvgGuestDirtyMB),
		strconv.Itoa(summary.CheckpointSamples),
		formatFloat(summary.AvgCheckpointAllocatedMB),
	})
	if err != nil {
		return fmt.Errorf("write summary row: %w", err)
	}
	o.summaryCSV.Flush()
	if err := o.summaryCSV.Error(); err != nil {
		return fmt.Errorf("flush summary row: %w", err)
	}

	return nil
}

func (o *memoryBenchmarkOutput) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var errs []error
	if o.summaryCSV != nil {
		o.summaryCSV.Flush()
		if err := o.summaryCSV.Error(); err != nil {
			errs = append(errs, err)
		}
	}
	if o.samplesFile != nil {
		errs = append(errs, o.samplesFile.Close())
		o.samplesFile = nil
	}
	if o.summaryFile != nil {
		errs = append(errs, o.summaryFile.Close())
		o.summaryFile = nil
	}

	return errors.Join(errs...)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}
