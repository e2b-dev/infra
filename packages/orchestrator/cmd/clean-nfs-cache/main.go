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
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/cleaner"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	serviceName    = "clean-nfs-cache"
	commitSHA      = ""
	serviceVersion = "0.1.0"
)

func main() {
	ctx := context.Background()
	var log logger.Logger
	var err error
	var opts cleaner.Options

	defer func() {
		if err != nil {
			if log != nil {
				log.Error(ctx, "NFS cache cleaner failed", zap.Error(err))
			} else {
				fmt.Println("NFS cache cleaner failed:", err)
			}
			os.Exit(1)
		}
	}()

	opts, log, err = preRun(ctx)
	if err != nil {
		return
	}
	defer log.Sync()

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
	)

	c := cleaner.NewCleaner(opts, log)
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

func preRun(ctx context.Context) (cleaner.Options, logger.Logger, error) {
	var opts cleaner.Options
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

	args := os.Args[1:] // skip the command name
	if err := flags.Parse(args); err != nil {
		return opts, nil, fmt.Errorf("could not parse flags: %w", err)
	}

	args = flags.Args()
	if len(args) != 1 {
		return opts, nil, ErrUsage
	}
	opts.Path = args[0]

	ffc, err := featureflags.NewClient()
	if err != nil {
		return opts, nil, err
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
	if opts.OtelCollectorEndpoint != "" {
		otelCore, err := newOtelCore(ctx, opts.OtelCollectorEndpoint)
		if err != nil {
			return opts, nil, fmt.Errorf("failed to create otel logger: %w", err)
		}
		cores = append(cores, otelCore)
	}

	l := utils.Must(logger.NewLogger(ctx, logger.LoggerConfig{
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
			return opts, nil, fmt.Errorf("could not get disk info: %w", err)
		}
		targetDiskUsage := uint64(opts.TargetDiskUsagePercent / 100 * float64(diskInfo.Total))
		if uint64(diskInfo.Used) > targetDiskUsage {
			opts.TargetBytesToDelete = uint64(diskInfo.Used) - targetDiskUsage
		}
	}

	return opts, l, nil
}

func newOtelCore(ctx context.Context, endpoint string) (zapcore.Core, error) {
	nodeID := env.GetNodeID()
	serviceInstanceID := uuid.NewString()

	resource, err := telemetry.GetResource(ctx, nodeID, serviceName, commitSHA, serviceVersion, serviceInstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	logsExporter, err := telemetry.NewLogExporter(ctx,
		otlploggrpc.WithEndpoint(endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create logs exporter: %w", err)
	}

	loggerProvider := telemetry.NewLogProvider(logsExporter, resource)
	otelCore := logger.GetOTELCore(loggerProvider, serviceName)

	return otelCore, nil
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
