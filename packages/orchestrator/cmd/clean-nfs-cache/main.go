package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/cleaner"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// meterExportPeriod controls how often the periodic reader flushes. Short
// enough that a typical cleaner run flushes at least once mid-flight (so we
// see metrics even if Shutdown's ForceFlush is interrupted), but not so
// short that we add meaningful CPU/network overhead.
const meterExportPeriod = 5 * time.Second

const (
	serviceName    = "clean-nfs-cache"
	commitSHA      = ""
	serviceVersion = "0.1.0"
)

func main() {
	ctx := context.Background()
	var log logger.Logger
	var err error
	var cfg runConfig

	var lp telemetry.LogProvider
	var mp *sdkmetric.MeterProvider
	var metrics *cleaner.Metrics
	cfg, log, lp, mp, metrics, err = preRun(ctx)
	opts := cfg.Cleaner
	if err != nil {
		fmt.Println("NFS cache cleaner failed:", err)
		if lp != nil {
			lp.Shutdown(ctx)
		}
		if mp != nil {
			mp.Shutdown(ctx)
		}
		os.Exit(1)
	}

	defer func() {
		if err != nil {
			log.Error(ctx, "NFS cache cleaner failed", zap.Error(err))
			defer os.Exit(1)
		}
		log.Sync()
		// Force-flush metrics before shutdown; this is a short-lived process
		// and the last batch of counters would otherwise be dropped.
		if mp != nil {
			flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = mp.ForceFlush(flushCtx)
			cancel()
			mp.Shutdown(ctx)
		}
		lp.Shutdown(ctx)
	}()

	if cfg.BenchEnabled {
		log.Info(ctx, "starting bench-shard-read",
			zap.String("path", cfg.Bench.Path),
			zap.Int("bench_build_ids", cfg.Bench.NumBuildIDs),
			zap.Int("bench_chunks_per_build", cfg.Bench.ChunksPerBuild),
			zap.Int("bench_file_size", cfg.Bench.FileSize),
			zap.Int("bench_concurrency", cfg.Bench.Concurrency),
			zap.Bool("bench_drop_caches", cfg.Bench.DropCaches),
			zap.Bool("bench_keep_artifacts", cfg.Bench.KeepArtifacts),
			zap.String("otel_collector_endpoint", opts.OtelCollectorEndpoint),
		)
		err = cleaner.RunBench(ctx, cfg.Bench, metrics, log)

		return
	}

	start := time.Now()
	log.Info(ctx, "starting",
		zap.Bool("dry_run", opts.DryRun),
		zap.Uint64("target_files_to_delete", opts.TargetFilesToDelete),
		zap.Uint64("target_bytes_to_delete", opts.TargetBytesToDelete),
		zap.Float64("target_disk_usage_percent", opts.TargetDiskUsagePercent),
		zap.Int("batch_n", opts.BatchN),
		zap.Int("delete_n", opts.DeleteN),
		zap.Int("max_retries", opts.MaxErrorRetries),
		zap.String("path", opts.Path),
		zap.String("otel_collector_endpoint", opts.OtelCollectorEndpoint),
		zap.Int("max_concurrent_stat", opts.MaxConcurrentStat),
		zap.Int("max_concurrent_scan", opts.MaxConcurrentScan),
		zap.Int("max_concurrent_delete", opts.MaxConcurrentDelete),
		zap.Duration("orphan_grace_period", cleaner.OrphanGracePeriod),
	)

	// preRun leaves both targets at 0 when disk usage is already at or
	// below disk-usage-target-percent. Short-circuit here so we don't spin
	// up workers just to immediately drain (which also produced a misleading
	// "target bytes deleted reached" log).
	if opts.TargetBytesToDelete == 0 && opts.TargetFilesToDelete == 0 {
		log.Info(ctx, "disk already at or below target, nothing to do",
			zap.Float64("target_disk_usage_percent", opts.TargetDiskUsagePercent))

		return
	}

	if err = cleaner.VerifyChunksCacheRoot(opts.Path); err != nil {
		return
	}

	c := cleaner.NewCleaner(opts, log, metrics)
	if err = c.Clean(ctx); err != nil {
		return
	}

	if c.RemoveC.Load() == 0 {
		log.Info(ctx, "no files deleted")

		return
	}

	mean, sd := standardDeviation(c.DeletedAge)
	dur := time.Since(start)
	filesPerSec := float64(c.RemoveC.Load()) / dur.Seconds()
	bytesPerSec := float64(c.DeletedBytes.Load()) / dur.Seconds()
	log.Info(ctx, "summary",
		zap.Bool("dry_run", opts.DryRun),
		zap.Int64("del_submitted", c.DeleteSubmittedC.Load()),
		zap.Int64("del_attempted", c.DeleteAttemptC.Load()),
		zap.Int64("del_already_gone", c.DeleteAlreadyGoneC.Load()),
		zap.Int64("del_err", c.DeleteErrC.Load()),
		zap.Int64("del_skip_changed", c.DeleteSkipC.Load()),
		zap.Int64("del_files", c.RemoveC.Load()),
		zap.Int64("empty_dirs", c.RemoveDirC.Load()),
		zap.Uint64("bytes", c.DeletedBytes.Load()),
		zap.Duration("most_recently_used", minDuration(c.DeletedAge).Round(time.Second)),
		zap.Duration("least_recently_used", maxDuration(c.DeletedAge).Round(time.Second)),
		zap.Duration("mean_age", mean.Round(time.Second)),
		zap.Float64("files_per_second", filesPerSec),
		zap.Float64("bytes_per_second", bytesPerSec),
		zap.Duration("duration", dur.Round(time.Second)),
		zap.Duration("std_deviation", sd.Round(time.Second)))
}

// runConfig bundles everything preRun parses, including the optional
// sharding A/B benchmark mode. Keeps preRun's return tuple small.
type runConfig struct {
	Cleaner      cleaner.Options
	Bench        cleaner.BenchOptions
	BenchEnabled bool
}

func preRun(ctx context.Context) (runConfig, logger.Logger, telemetry.LogProvider, *sdkmetric.MeterProvider, *cleaner.Metrics, error) {
	var cfg runConfig
	opts := &cfg.Cleaner
	var featureFlagPresent bool

	flags := flag.NewFlagSet("clean-nfs-cache", flag.ExitOnError)
	flags.Uint64Var(&opts.TargetFilesToDelete, "target-files-to-delete", 0, "target number of files to delete (overrides disk-usage-target-percent and target-bytes-to-delete)")
	flags.Uint64Var(&opts.TargetBytesToDelete, "target-bytes-to-delete", 0, "target number of bytes to delete (overrides disk-usage-target-percent)")
	flags.Float64Var(&opts.TargetDiskUsagePercent, "disk-usage-target-percent", 90, "disk usage target as a % (0-100)")
	flags.BoolVar(&opts.DryRun, "dry-run", true, "dry run")
	flags.IntVar(&opts.BatchN, "files-per-loop", 10000, "number of files to gather metadata for per loop")
	flags.IntVar(&opts.DeleteN, "deletions-per-loop", 100, "maximum number of files to delete per loop")
	flags.StringVar(&opts.OtelCollectorEndpoint, "otel-collector-endpoint", "", "endpoint of the otel collector")
	flags.IntVar(&opts.MaxConcurrentStat, "max-concurrent-stat", 1, "number of concurrent stat goroutines")
	flags.IntVar(&opts.MaxConcurrentScan, "max-concurrent-scan", 1, "number of concurrent scanner goroutines")
	flags.IntVar(&opts.MaxConcurrentDelete, "max-concurrent-delete", 1, "number of concurrent deleter goroutines")
	flags.IntVar(&opts.MaxErrorRetries, "max-retries", 10, "maximum number of continuous error or miss retries before giving up")

	flags.BoolVar(&cfg.BenchEnabled, "bench-shard-read", false, "run the flat-vs-sharded read-path benchmark instead of cleaning up; bench artifacts live under <path>/bench-shard-read/")
	flags.IntVar(&cfg.Bench.NumBuildIDs, "bench-build-ids", 200, "number of synthetic BuildID dirs per layout in --bench-shard-read mode")
	flags.IntVar(&cfg.Bench.ChunksPerBuild, "bench-chunks-per-build", 50, "number of synthetic chunk files per BuildID in --bench-shard-read mode")
	flags.IntVar(&cfg.Bench.FileSize, "bench-file-size", 4096, "size of each synthetic chunk file in bytes; small is fine, the bench targets metadata cost, not throughput")
	flags.IntVar(&cfg.Bench.Concurrency, "bench-concurrency", 8, "number of goroutines for the parallel-within-build scenario")
	flags.BoolVar(&cfg.Bench.DropCaches, "bench-drop-caches", false, "drop kernel FS caches between cold runs (requires root; on failure the bench logs and proceeds)")
	flags.BoolVar(&cfg.Bench.KeepArtifacts, "bench-keep-artifacts", false, "do not RemoveAll the synthetic bench tree on exit; useful for post-hoc inspection")

	args := os.Args[1:] // skip the command name
	if err := flags.Parse(args); err != nil {
		return cfg, nil, nil, nil, nil, fmt.Errorf("could not parse flags: %w", err)
	}

	args = flags.Args()
	if len(args) != 1 {
		return cfg, nil, nil, nil, nil, ErrUsage
	}
	opts.Path = args[0]
	cfg.Bench.Path = args[0]

	ffc, err := featureflags.NewClient()
	if err != nil {
		return cfg, nil, nil, nil, nil, err
	}
	defer ffc.Close(ctx)

	v := ffc.JSONFlag(ctx, featureflags.CleanNFSCache)
	featureFlagPresent = v.Type() == ldvalue.ObjectType
	if featureFlagPresent {
		m := v.AsValueMap()
		if m.Get("maxConcurrentDelete").IsNumber() {
			opts.MaxConcurrentDelete = m.Get("maxConcurrentDelete").IntValue()
		}
		if m.Get("maxConcurrentScan").IsNumber() {
			opts.MaxConcurrentScan = m.Get("maxConcurrentScan").IntValue()
		}
		if m.Get("maxConcurrentStat").IsNumber() {
			opts.MaxConcurrentStat = m.Get("maxConcurrentStat").IntValue()
		}
		if m.Get("maxRetries").IsNumber() {
			opts.MaxErrorRetries = m.Get("maxRetries").IntValue()
		}
		if m.Get("targetBytesToDelete").IsNumber() {
			opts.TargetBytesToDelete = uint64(m.Get("targetBytesToDelete").Float64Value())
		}
		if m.Get("targetFilesToDelete").IsNumber() {
			opts.TargetFilesToDelete = uint64(m.Get("targetFilesToDelete").Float64Value())
		}
	}

	var cores []zapcore.Core
	logProvider := telemetry.NewNoopLogProvider()
	var meterProvider *sdkmetric.MeterProvider
	// Default to noop metrics so cleaner methods are safe to call when no
	// collector endpoint is configured (e.g. local dev).
	metrics := cleaner.NoopMetrics()
	if opts.OtelCollectorEndpoint != "" {
		var otelCore zapcore.Core
		var err error
		otelCore, logProvider, err = newOtelCore(ctx, opts.OtelCollectorEndpoint)
		if err != nil {
			return cfg, nil, nil, nil, nil, fmt.Errorf("failed to create otel logger: %w", err)
		}
		cores = append(cores, otelCore)

		meterProvider, metrics, err = newOtelMetrics(ctx, opts.OtelCollectorEndpoint)
		if err != nil {
			logProvider.Shutdown(ctx)
			return cfg, nil, nil, nil, nil, fmt.Errorf("failed to create otel metrics: %w", err)
		}
	}

	l := utils.Must(logger.NewLogger(logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         cores,
		EnableConsole: true,
	}))

	if featureFlagPresent {
		l.Info(ctx, "feature flag present", zap.String("flag", featureflags.CleanNFSCache.String()))
	}

	if opts.TargetBytesToDelete == 0 && opts.TargetFilesToDelete == 0 && opts.TargetDiskUsagePercent > 0 {
		var diskInfo cleaner.DiskInfo
		var err error
		timeit(ctx, fmt.Sprintf("getting disk info for %q", opts.Path), func() {
			diskInfo, err = cleaner.GetDiskInfo(ctx, opts.Path)
		})
		if err != nil {
			if logProvider != nil {
				logProvider.Shutdown(ctx)
			}
			if meterProvider != nil {
				meterProvider.Shutdown(ctx)
			}

			return cfg, nil, nil, nil, nil, fmt.Errorf("could not get disk info: %w", err)
		}
		targetDiskUsage := uint64(opts.TargetDiskUsagePercent / 100 * float64(diskInfo.Total))
		if uint64(diskInfo.Used) > targetDiskUsage {
			opts.TargetBytesToDelete = uint64(diskInfo.Used) - targetDiskUsage
		}
	}

	return cfg, l, logProvider, meterProvider, metrics, nil
}

// newOtelMetrics mirrors newOtelCore but for the metric pipeline. Returns the
// MeterProvider so main can flush and shut it down on exit, plus the
// pre-built Metrics struct holding all cleaner instruments.
func newOtelMetrics(ctx context.Context, endpoint string) (*sdkmetric.MeterProvider, *cleaner.Metrics, error) {
	nodeID := env.GetNodeID()
	serviceInstanceID := uuid.NewString()

	res, err := telemetry.GetResource(ctx, nodeID, serviceName, commitSHA, serviceVersion, serviceInstanceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	exporter, err := telemetry.NewMeterExporter(ctx, otlpmetricgrpc.WithEndpoint(endpoint))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create meter exporter: %w", err)
	}

	mp, err := telemetry.NewMeterProvider(exporter, meterExportPeriod, res)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create meter provider: %w", err)
	}

	// telemetry.NewMeterProvider returns the metric.MeterProvider interface,
	// but our caller needs the concrete *sdkmetric.MeterProvider for
	// ForceFlush/Shutdown. Type-assert here so the dependency is local.
	sdkmp, ok := mp.(*sdkmetric.MeterProvider)
	if !ok {
		return nil, nil, fmt.Errorf("meter provider was not *sdkmetric.MeterProvider: %T", mp)
	}

	m, err := cleaner.NewMetrics(sdkmp.Meter("github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/cleaner"))
	if err != nil {
		sdkmp.Shutdown(ctx)
		return nil, nil, fmt.Errorf("failed to create cleaner metrics: %w", err)
	}

	return sdkmp, m, nil
}

func newOtelCore(ctx context.Context, endpoint string) (zapcore.Core, telemetry.LogProvider, error) {
	nodeID := env.GetNodeID()
	serviceInstanceID := uuid.NewString()

	resource, err := telemetry.GetResource(ctx, nodeID, serviceName, commitSHA, serviceVersion, serviceInstanceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	logProvider, err := telemetry.NewLogProvider(ctx, resource, otlploggrpc.WithEndpoint(endpoint))
	if err != nil {
		return nil, nil, err
	}
	otelCore := logger.GetOTELCore(logProvider, serviceName)

	return otelCore, logProvider, nil
}

func standardDeviation(accessed []time.Duration) (mean, stddev time.Duration) {
	if len(accessed) == 0 {
		return 0, 0
	}

	var sum float64
	for i := range accessed {
		sum += float64(accessed[i])
	}
	mean = time.Duration(sum / float64(len(accessed)))

	var sd float64
	for i := range accessed {
		sd += math.Pow(float64(accessed[i]-mean), 2)
	}

	sd = math.Sqrt(sd / float64(len(accessed)))

	return mean, time.Duration(sd)
}

func maxDuration(durations []time.Duration) time.Duration {
	return loop(durations, func(one, two time.Duration) bool {
		return one > two
	})
}

func minDuration(durations []time.Duration) time.Duration {
	return loop(durations, func(one, two time.Duration) bool {
		return one < two
	})
}

func loop[T any](items []T, betterThan func(one, two T) bool) T {
	if len(items) == 0 {
		var t T

		return t
	}

	if len(items) == 1 {
		return items[0]
	}

	var best int
	for current := 1; current < len(items); current++ {
		if betterThan(items[current], items[best]) {
			best = current
		}
	}

	return items[best]
}

var ErrUsage = errors.New("usage: clean-nfs-cache <path> [<options>]")

func timeit(ctx context.Context, message string, fn func()) {
	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	logger.L().Debug(ctx, message, zap.Duration("duration", done))
}
