package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/ex"
	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/pkg"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func main() {
	ctx := context.Background()
	var log logger.Logger
	var err error
	var opts ex.Options

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
	if !opts.Experimental {
		main1()
		return
	}

	c := ex.NewCleaner(opts, log)
	if err = c.Clean(ctx); err != nil {
		return
	}

	// c.PrintSummary(ctx)
}

func preRun(ctx context.Context) (ex.Options, logger.Logger, error) {
	var opts ex.Options

	flags := flag.NewFlagSet("clean-nfs-cache", flag.ExitOnError)
	flags.Float64Var(&opts.TargetDiskUsagePercent, "disk-usage-target-percent", 90, "disk usage target as a % (0-100)")
	flags.BoolVar(&opts.DryRun, "dry-run", true, "dry run")
	flags.IntVar(&opts.BatchN, "files-per-loop", 10000, "number of files to gather metadata for per loop")
	flags.IntVar(&opts.DeleteN, "deletions-per-loop", 100, "maximum number of files to delete per loop")
	flags.StringVar(&opts.OtelCollectorEndpoint, "otel-collector-endpoint", "", "endpoint of the otel collector")
	flags.BoolVar(&opts.AggressiveStat, "aggressive-stat", false, "use aggressive stat calls to get file metadata")
	flags.IntVar(&opts.NumScanners, "num-scanners", 16, "number of concurrent scanner goroutines")
	flags.IntVar(&opts.NumDeleters, "num-deleters", 4, "number of concurrent deleter goroutines")
	flags.IntVar(&opts.MaxErrorRetries, "max-error-retries", 10, "maximum number of continuous error retries before giving up")
	flags.Uint64Var(&opts.TargetBytesToDelete, "target-bytes-to-delete", 0, "target number of bytes to delete (overrides disk-usage-target-percent if set)")
	flags.BoolVar(&opts.Experimental, "experimental", false, "enable experimental features")

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

	v, err := ffc.JSONFlag(ctx, featureflags.CleanNFSCacheExperimental)
	if err != nil {
		return opts, nil, err
	}

	if v.Type() == ldvalue.ObjectType {
		m := v.AsValueMap()
		if m.Get("experimental").IsBool() {
			opts.Experimental = m.Get("experimental").BoolValue()
		}

		opts.Experimental = true // REMOVE ME!!!

		if opts.Experimental {
			if opts.NumDeleters == 0 && m.Get("deleters").IsInt() {
				opts.NumDeleters = m.Get("deleters").IntValue()
			}
			if opts.NumScanners == 0 && m.Get("scanners").IsInt() {
				opts.NumScanners = m.Get("scanners").IntValue()
			}
			if opts.MaxErrorRetries == 0 && m.Get("maxErrorRetries").IsInt() {
				opts.MaxErrorRetries = m.Get("maxErrorRetries").IntValue()
			}
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

	if opts.TargetBytesToDelete == 0 {
		var diskInfo pkg.DiskInfo
		var err error
		timeit(ctx, fmt.Sprintf("getting disk info for %q", opts.Path), func() {
			diskInfo, err = pkg.GetDiskInfo(ctx, opts.Path)
		})
		if err != nil {
			return opts, nil, fmt.Errorf("could not get disk info: %w", err)
		}
		targetDiskUsage := uint64(opts.TargetDiskUsagePercent / 100 * float64(diskInfo.Total))
		opts.TargetBytesToDelete = uint64(diskInfo.Used) - targetDiskUsage
	}

	return opts, l, nil
}
