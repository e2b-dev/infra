package cleaner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Scenario value strings live next to the only code that records them.
const (
	ValScenarioColdCrossBuild = "cold_cross_build"
	ValScenarioWarmSameBuild  = "warm_same_build"
	ValScenarioParallel       = "parallel_within_build"
)

// BenchOptions configures the sharding A/B read-path benchmark. The benchmark
// lays down identical synthetic data in two sibling subtrees under
// <Path>/bench-shard-read/ — one flat, one sharded — then exercises read paths
// against each and records per-read latency to OTEL plus a summary log.
type BenchOptions struct {
	// Path is the chunks-cache root (the same path the cleaner normally
	// operates on). All bench artifacts are scoped to a "bench-shard-read"
	// subdirectory under it, never touching real cache contents.
	Path string

	// NumBuildIDs is how many synthetic BuildID dirs to create per layout.
	// The cold_cross_build scenario opens one chunk per BuildID so this
	// directly drives that scenario's sample count and is the variable to
	// scale up when probing the realistic Filestore directory size.
	NumBuildIDs int

	// ChunksPerBuild is how many chunk files to create per BuildID.
	ChunksPerBuild int

	// FileSize is the size (bytes) of each synthetic chunk file. Small files
	// are fine — the benchmark targets metadata + small-read latency, not
	// throughput.
	FileSize int

	// Concurrency is the number of goroutines for the parallel scenario.
	Concurrency int

	// SetupConcurrency is the number of goroutines used to lay down the
	// synthetic data tree. NFS file creation is dominated by per-op RTT,
	// so concurrency is the main lever for keeping setup time reasonable
	// at scale.
	SetupConcurrency int

	// DropCaches asks the benchmark to drop kernel filesystem caches between
	// cold-phase runs by writing to /proc/sys/vm/drop_caches. Requires root.
	// On failure the benchmark logs a warning and proceeds (warm-ish run).
	DropCaches bool

	// KeepArtifacts skips post-run RemoveAll of the bench tree. Useful when
	// you want to inspect the layout manually after the fact.
	KeepArtifacts bool
}

// RunBench performs the sharding A/B benchmark and reports results.
// The OTEL Metrics, if non-nil, receive per-open timings tagged with
// {layout, scenario}; a noop *Metrics is acceptable for local runs.
func RunBench(ctx context.Context, opts BenchOptions, metrics *Metrics, log logger.Logger) error {
	if metrics == nil {
		metrics = NoopMetrics()
	}
	if err := validateBenchOptions(&opts); err != nil {
		return err
	}

	benchRoot := filepath.Join(opts.Path, "bench-shard-read")
	log.Info(ctx, "bench: setup",
		zap.String("root", benchRoot),
		zap.Int("build_ids", opts.NumBuildIDs),
		zap.Int("chunks_per_build", opts.ChunksPerBuild),
		zap.Int("file_size", opts.FileSize),
		zap.Int("concurrency", opts.Concurrency),
		zap.Bool("drop_caches", opts.DropCaches),
	)

	flatRoot := filepath.Join(benchRoot, "flat")
	shardedRoot := filepath.Join(benchRoot, "sharded")

	if !opts.KeepArtifacts {
		defer func() {
			if rmErr := os.RemoveAll(benchRoot); rmErr != nil {
				log.Info(ctx, "bench: teardown failed", zap.Error(rmErr))
			}
		}()
	}

	buildIDs, err := setupBenchData(ctx, opts, flatRoot, shardedRoot, log)
	if err != nil {
		return fmt.Errorf("bench setup: %w", err)
	}

	results := make(map[string]map[string]*scenarioStats) // layout → scenario → stats

	for _, layout := range []struct {
		name string
		root string
	}{
		{ValLayoutFlat, flatRoot},
		{ValLayoutSharded, shardedRoot},
	} {
		layoutResults := map[string]*scenarioStats{}

		log.Info(ctx, "bench: running cold cross-build scenario", zap.String("layout", layout.name))
		tryDropCaches(ctx, opts, log)
		layoutResults[ValScenarioColdCrossBuild] = runColdCrossBuild(ctx, layout.name, layout.root, opts, buildIDs, metrics)

		log.Info(ctx, "bench: running warm same-build scenario", zap.String("layout", layout.name))
		layoutResults[ValScenarioWarmSameBuild] = runWarmSameBuild(ctx, layout.name, layout.root, opts, buildIDs, metrics)

		log.Info(ctx, "bench: running parallel within-build scenario", zap.String("layout", layout.name))
		tryDropCaches(ctx, opts, log)
		layoutResults[ValScenarioParallel] = runParallelWithinBuild(ctx, layout.name, layout.root, opts, buildIDs, metrics)

		results[layout.name] = layoutResults
	}

	reportSummary(ctx, log, results)

	return nil
}

func validateBenchOptions(opts *BenchOptions) error {
	var errs []error
	if opts.Path == "" {
		errs = append(errs, errors.New("Path is required"))
	}
	if opts.NumBuildIDs <= 0 {
		errs = append(errs, errors.New("NumBuildIDs must be > 0"))
	}
	if opts.ChunksPerBuild <= 0 {
		errs = append(errs, errors.New("ChunksPerBuild must be > 0"))
	}
	if opts.FileSize < 0 {
		errs = append(errs, errors.New("FileSize must be >= 0"))
	}
	if opts.Concurrency <= 0 {
		errs = append(errs, errors.New("Concurrency must be > 0"))
	}
	if opts.SetupConcurrency <= 0 {
		// Permissive default: callers (tests, ad-hoc) may not bother to set it.
		opts.SetupConcurrency = 32
	}

	return errors.Join(errs...)
}

// shardPathFor returns the two-segment {aa}/{bb} prefix for a BuildID UUID,
// using the first two and next two hex characters. Matches the sharding
// scheme proposed for the chunks-cache layout redesign.
func shardPathFor(buildID string) string {
	if len(buildID) < 4 {
		return buildID
	}

	return filepath.Join(buildID[:2], buildID[2:4])
}

// flatBuildDir returns {flatRoot}/{BuildID}/memfile/.
func flatBuildDir(flatRoot, buildID string) string {
	return filepath.Join(flatRoot, buildID, "memfile")
}

// shardedBuildDir returns {shardedRoot}/{aa}/{bb}/{BuildID}/memfile/.
func shardedBuildDir(shardedRoot, buildID string) string {
	return filepath.Join(shardedRoot, shardPathFor(buildID), buildID, "memfile")
}

// chunkFilename matches the storage.cachedSeekable convention so that the
// bench data is structurally indistinguishable from a real chunks-cache.
func chunkFilename(index, fileSize int) string {
	return fmt.Sprintf("%012d-%d.bin", index, fileSize)
}

// setupBenchData creates NumBuildIDs UUID-named directories under both
// flat and sharded roots, each populated with ChunksPerBuild chunk files
// of FileSize bytes. Returns the list of BuildIDs used.
//
// Setup is parallelized across SetupConcurrency goroutines because NFS file
// creation is RTT-bound; at 10K+ BuildIDs a sequential loop would take
// minutes per layout.
func setupBenchData(ctx context.Context, opts BenchOptions, flatRoot, shardedRoot string, log logger.Logger) ([]string, error) {
	buildIDs := make([]string, opts.NumBuildIDs)
	for i := range buildIDs {
		buildIDs[i] = uuid.NewString()
	}

	payload := make([]byte, opts.FileSize)
	if opts.FileSize > 0 {
		if _, err := rand.Read(payload); err != nil {
			return nil, fmt.Errorf("generate payload: %w", err)
		}
	}

	start := time.Now()

	jobs := make(chan string, opts.SetupConcurrency*2)
	errCh := make(chan error, opts.SetupConcurrency)

	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for b := range jobs {
			for _, dir := range []string{flatBuildDir(flatRoot, b), shardedBuildDir(shardedRoot, b)} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					select {
					case errCh <- fmt.Errorf("mkdir %s: %w", dir, err):
					default:
					}

					return
				}
				for i := 0; i < opts.ChunksPerBuild; i++ {
					p := filepath.Join(dir, chunkFilename(i, opts.FileSize))
					if err := os.WriteFile(p, payload, 0o644); err != nil {
						select {
						case errCh <- fmt.Errorf("write %s: %w", p, err):
						default:
						}

						return
					}
				}
			}
		}
	}

	for i := 0; i < opts.SetupConcurrency; i++ {
		wg.Add(1)
		go worker()
	}

	go func() {
		defer close(jobs)
		for _, b := range buildIDs {
			select {
			case <-ctx.Done():
				return
			case jobs <- b:
			}
		}
	}()

	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	log.Info(ctx, "bench: setup complete",
		zap.Int("total_files", opts.NumBuildIDs*opts.ChunksPerBuild*2),
		zap.Int("setup_concurrency", opts.SetupConcurrency),
		zap.Duration("duration", time.Since(start)))

	return buildIDs, nil
}

// tryDropCaches best-effort drops the kernel page+inode+dentry caches so
// the following scenario is genuinely cold. Requires root; on failure we
// log a single warning per call and proceed.
func tryDropCaches(ctx context.Context, opts BenchOptions, log logger.Logger) {
	if !opts.DropCaches {
		return
	}
	if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0o200); err != nil {
		log.Info(ctx, "bench: failed to drop caches (need root); scenario may be warmer than intended",
			zap.Error(err))
	}
}

// scenarioStats accumulates per-open timings for one (layout, scenario) pair.
type scenarioStats struct {
	durations []time.Duration
	errs      int
}

func (s *scenarioStats) record(d time.Duration, err error) {
	s.durations = append(s.durations, d)
	if err != nil {
		s.errs++
	}
}

// percentiles returns p50, p99, max from the recorded durations. Returns
// zeroes when no samples were recorded.
func (s *scenarioStats) percentiles() (p50, p99, max time.Duration) {
	if len(s.durations) == 0 {
		return 0, 0, 0
	}
	sorted := append([]time.Duration(nil), s.durations...)
	slices.Sort(sorted)
	idx := func(p float64) int {
		i := int(math.Round(p * float64(len(sorted)-1)))
		if i < 0 {
			i = 0
		}
		if i >= len(sorted) {
			i = len(sorted) - 1
		}

		return i
	}

	return sorted[idx(0.50)], sorted[idx(0.99)], sorted[len(sorted)-1]
}

func (s *scenarioStats) mean() time.Duration {
	if len(s.durations) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range s.durations {
		sum += d
	}

	return sum / time.Duration(len(s.durations))
}

// recordRead times one os.ReadFile call and emits OTEL + accumulates stats.
// ReadFile (vs Open) mirrors what the orchestrator actually does on a cache
// hit: LOOKUP + OPEN + READ + CLOSE in one round of syscalls. For small
// payloads the result is metadata-bound, but using ReadFile keeps the
// benchmark honest about what we're really measuring.
func recordRead(ctx context.Context, path string, attrs []attribute.KeyValue, metrics *Metrics, stats *scenarioStats) {
	start := time.Now()
	_, err := os.ReadFile(path)
	elapsed := time.Since(start)
	stats.record(elapsed, err)

	metrics.BenchReadDuration.Record(ctx, elapsed.Microseconds(), metric.WithAttributes(attrs...))
	result := ValResultOk
	if err != nil {
		result = ValResultErr
	}
	resultAttrs := append([]attribute.KeyValue{}, attrs...)
	resultAttrs = append(resultAttrs, attribute.String(AttrResult, result))
	metrics.BenchReadOps.Add(ctx, 1, metric.WithAttributes(resultAttrs...))
}

// runColdCrossBuild opens one chunk from each BuildID in shuffled order. This
// is the worst case for sharded layouts on a cold attr cache: every open
// traverses a different intermediate directory tree.
func runColdCrossBuild(ctx context.Context, layout, root string, opts BenchOptions, buildIDs []string, metrics *Metrics) *scenarioStats {
	attrs := []attribute.KeyValue{
		attribute.String(AttrLayout, layout),
		attribute.String(AttrScenario, ValScenarioColdCrossBuild),
	}
	stats := &scenarioStats{}

	order := mathrand.Perm(len(buildIDs))
	for _, idx := range order {
		if err := ctx.Err(); err != nil {
			return stats
		}
		b := buildIDs[idx]
		p := filepath.Join(buildDirFor(layout, root, b), chunkFilename(0, opts.FileSize))
		recordRead(ctx, p, attrs, metrics, stats)
	}

	return stats
}

// runWarmSameBuild opens every chunk of one BuildID in order. The attribute
// cache should hit on the leaf directory after the first open, so the per-open
// cost converges to the warm minimum and the layout depth should not matter.
func runWarmSameBuild(ctx context.Context, layout, root string, opts BenchOptions, buildIDs []string, metrics *Metrics) *scenarioStats {
	attrs := []attribute.KeyValue{
		attribute.String(AttrLayout, layout),
		attribute.String(AttrScenario, ValScenarioWarmSameBuild),
	}
	stats := &scenarioStats{}

	b := buildIDs[0]
	dir := buildDirFor(layout, root, b)
	for i := 0; i < opts.ChunksPerBuild; i++ {
		if err := ctx.Err(); err != nil {
			return stats
		}
		recordRead(ctx, filepath.Join(dir, chunkFilename(i, opts.FileSize)), attrs, metrics, stats)
	}

	return stats
}

// runParallelWithinBuild fires Concurrency goroutines that each open every
// chunk in one BuildID. Mimics NBD's pattern where many block reads target
// the same data file in parallel.
func runParallelWithinBuild(ctx context.Context, layout, root string, opts BenchOptions, buildIDs []string, metrics *Metrics) *scenarioStats {
	attrs := []attribute.KeyValue{
		attribute.String(AttrLayout, layout),
		attribute.String(AttrScenario, ValScenarioParallel),
	}

	b := buildIDs[0]
	dir := buildDirFor(layout, root, b)

	statsMu := sync.Mutex{}
	combined := &scenarioStats{}

	var wg sync.WaitGroup
	wg.Add(opts.Concurrency)
	for g := 0; g < opts.Concurrency; g++ {
		go func() {
			defer wg.Done()
			local := &scenarioStats{}
			for i := 0; i < opts.ChunksPerBuild; i++ {
				if err := ctx.Err(); err != nil {
					return
				}
				recordRead(ctx, filepath.Join(dir, chunkFilename(i, opts.FileSize)), attrs, metrics, local)
			}
			statsMu.Lock()
			combined.durations = append(combined.durations, local.durations...)
			combined.errs += local.errs
			statsMu.Unlock()
		}()
	}
	wg.Wait()

	return combined
}

func buildDirFor(layout, root, buildID string) string {
	if layout == ValLayoutSharded {
		return shardedBuildDir(root, buildID)
	}

	return flatBuildDir(root, buildID)
}

// reportSummary prints a layout × scenario comparison table to the log.
func reportSummary(ctx context.Context, log logger.Logger, results map[string]map[string]*scenarioStats) {
	scenarios := []string{ValScenarioColdCrossBuild, ValScenarioWarmSameBuild, ValScenarioParallel}
	for _, s := range scenarios {
		flat := results[ValLayoutFlat][s]
		sharded := results[ValLayoutSharded][s]
		fields := []zap.Field{
			zap.String("scenario", s),
		}
		if flat != nil {
			meanF := flat.mean()
			p50F, p99F, maxF := flat.percentiles()
			fields = append(fields,
				zap.Int("flat_samples", len(flat.durations)),
				zap.Int("flat_errs", flat.errs),
				zap.Duration("flat_mean", meanF),
				zap.Duration("flat_p50", p50F),
				zap.Duration("flat_p99", p99F),
				zap.Duration("flat_max", maxF),
			)
		}
		if sharded != nil {
			meanS := sharded.mean()
			p50S, p99S, maxS := sharded.percentiles()
			fields = append(fields,
				zap.Int("sharded_samples", len(sharded.durations)),
				zap.Int("sharded_errs", sharded.errs),
				zap.Duration("sharded_mean", meanS),
				zap.Duration("sharded_p50", p50S),
				zap.Duration("sharded_p99", p99S),
				zap.Duration("sharded_max", maxS),
			)
		}
		if flat != nil && sharded != nil && flat.mean() > 0 {
			deltaPct := float64(sharded.mean()-flat.mean()) / float64(flat.mean()) * 100
			fields = append(fields, zap.Float64("sharded_vs_flat_mean_pct", deltaPct))
		}
		log.Info(ctx, "bench: result", fields...)
	}
}
