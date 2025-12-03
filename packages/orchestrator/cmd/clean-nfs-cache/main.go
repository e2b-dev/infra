package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/pkg"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	serviceName    = "clean-nfs-cache"
	commitSHA      = ""
	serviceVersion = "0.1.0"
)

func main1() {
	ctx := context.Background()
	if _, err := cleanNFSCache(ctx, os.Args, 0); err != nil {
		logger.L().Error(ctx, "clean NFS cache failed", zap.Error(err))
		os.Exit(1)
	}
}

func cleanNFSCache(ctx context.Context, args []string, targetBytesToDelete int64) (results, error) {
	var allResults results
	path, opts, err := parseArgs(args)
	if err != nil {
		return allResults, fmt.Errorf("invalid arguments: %w", err)
	}

	var cores []zapcore.Core
	if opts.otelCollectorEndpoint != "" {
		otelCore, err := newOtelCore(ctx, opts.otelCollectorEndpoint)
		if err != nil {
			return allResults, fmt.Errorf("failed to create otel logger: %w", err)
		}
		cores = append(cores, otelCore)
	}

	globalLogger := utils.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         cores,
		EnableConsole: true,
	}))
	defer func(l logger.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down logger: %v", err)
		}
	}(globalLogger)
	logger.ReplaceGlobals(ctx, globalLogger)

	// get free space information for path
	logger.L().Info(ctx, "starting",
		zap.Bool("dry_run", opts.dryRun),
		zap.Float64("target_percent", opts.targetDiskUsagePercent),
		zap.String("path", path))

	var diskInfo pkg.DiskInfo
	timeit(ctx, fmt.Sprintf("getting disk info for %q", path), func() {
		diskInfo, err = pkg.GetDiskInfo(ctx, path)
	})
	if err != nil {
		return allResults, fmt.Errorf("could not get disk info: %w", err)
	}
	targetDiskUsage := int64(float64(opts.targetDiskUsagePercent) / 100 * float64(diskInfo.Total))
	// for testing
	if targetBytesToDelete > 0 {
		targetDiskUsage = diskInfo.Used - int64(targetBytesToDelete)
	}
	areWeDone := func() bool {
		return diskInfo.Used < targetDiskUsage
	}

	cache := pkg.NewListingCache(path)

	defer func() {
		printSummary(ctx, allResults, opts)
	}()

	// if conditions are met, we're done
	for !areWeDone() {
		// get File metadata, including path, size, and last access timestamp
		var files []pkg.File
		timeit(ctx, fmt.Sprintf("gathering metadata on %d files", opts.filesPerLoop), func() {
			files, err = getFiles(ctx, cache, opts.filesPerLoop)
			logger.L().Info(ctx, "got files", zap.Int("count", len(files)))
		})
		if err != nil {
			return allResults, fmt.Errorf("could not get File metadata: %w", err)
		}

		// sort files by access timestamp
		timeit(ctx, fmt.Sprintf("sorting %d files by access time", len(files)), func() {
			sortFilesByATime(files)
		})

		var results results
		timeit(ctx, fmt.Sprintf("deleting bottom %d files", opts.filesToDeletePerLoop), func() {
			results, err = deleteOldestFiles(ctx, cache, files, opts, &diskInfo, areWeDone, opts.filesToDeletePerLoop)
			logger.L().Info(ctx, "deleted files",
				zap.Int64("count", results.deletedFiles),
				zap.Int64("bytes", results.deletedBytes))
		})
		allResults = allResults.sum(results)
		if err != nil {
			return allResults, fmt.Errorf("failed to delete files: %w", err)
		}
	}

	return allResults, nil
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

func printSummary(ctx context.Context, r results, opts opts) {
	if r.deletedFiles == 0 {
		logger.L().Info(ctx, "no files deleted")

		return
	}

	_, sd := standardDeviation(r.lastAccessed)

	logger.L().Info(ctx, "summary",
		zap.Bool("dry_run", opts.dryRun),
		zap.Int64("files", r.deletedFiles),
		zap.Int64("bytes", r.deletedBytes),
		zap.Duration("most_recently_used", minDuration(r.lastAccessed).Round(time.Second)),
		zap.Duration("least_recently_used", maxDuration(r.lastAccessed).Round(time.Second)),
		zap.Duration("std_deviation", sd.Round(time.Second)))
}

func standardDeviation(accessed []time.Duration) (mean, stddev time.Duration) {
	if len(accessed) == 0 {
		return 0, 0
	}

	var sum time.Duration
	for i := range accessed {
		sum += accessed[i]
	}
	mean = sum / time.Duration(len(accessed))

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

type results struct {
	deletedFiles     int64
	deletedBytes     int64
	lastAccessed     []time.Duration
	createdDurations []time.Duration
}

func (r results) sum(other results) results {
	return results{
		deletedFiles:     r.deletedFiles + other.deletedFiles,
		deletedBytes:     r.deletedBytes + other.deletedBytes,
		lastAccessed:     append(r.lastAccessed, other.lastAccessed...),
		createdDurations: append(r.createdDurations, other.createdDurations...),
	}
}

func deleteOldestFiles(ctx context.Context, cache *pkg.ListingCache, files []pkg.File, opts opts, diskInfo *pkg.DiskInfo, areWeDone func() bool, deleteCount int64) (results, error) {
	now := time.Now()
	var results results
	for _, file := range files {
		if !opts.dryRun {
			if err := os.Remove(file.Path); err != nil {
				logger.L().Error(ctx, "failed to delete",
					zap.String("path", file.Path),
					zap.Error(err))

				continue
			}
		}

		cache.Decache(file.Path)
		results.deletedFiles++
		results.deletedBytes += file.Size
		results.lastAccessed = append(results.lastAccessed, now.Sub(file.ATime))
		results.createdDurations = append(results.createdDurations, time.Since(file.BTime))

		// record the File as free space
		diskInfo.Used -= file.Size
		if areWeDone() || results.deletedFiles >= deleteCount {
			// we're done!
			return results, nil
		}
	}

	return results, fmt.Errorf("%w: target: %.2f%% < actual: %.2f%%",
		ErrFail, opts.targetDiskUsagePercent,
		(float64(diskInfo.Used)/float64(diskInfo.Total))*100)
}

func sortFilesByATime(files []pkg.File) {
	sort.Slice(files, func(i, j int) bool {
		return files[j].ATime.After(files[i].ATime)
	})
}

func reportGetFilesProgress(ctx context.Context, usedFiles, dupeHits int) {
	total := usedFiles + dupeHits
	if total > 0 && total%100 == 0 {
		logger.L().Debug(ctx, "gathering files progress",
			zap.Int("files", usedFiles),
			zap.Int("dupe_hits", dupeHits))
	}
}

func getFiles(ctx context.Context, cache *pkg.ListingCache, maxFiles int) ([]pkg.File, error) {
	var items []pkg.File

	usedFiles := make(map[string]struct{})
	var dupeHits int

	for len(items) != maxFiles {
		reportGetFilesProgress(ctx, len(usedFiles), dupeHits)

		path, err := cache.GetRandomFile()
		if err != nil {
			return nil, err
		}

		if _, ok := usedFiles[path]; ok {
			dupeHits++
			if dupeHits == maxFiles {
				return items, nil // we found too many repeats, we're done
			}

			continue
		}

		metadata, err := pkg.GetFileMetadata(path)
		if err != nil {
			return nil, err
		}

		items = append(items, metadata)
		usedFiles[path] = struct{}{}
	}

	reportGetFilesProgress(ctx, len(usedFiles), dupeHits)

	return items, nil
}

type opts struct {
	targetDiskUsagePercent float64
	dryRun                 bool
	filesPerLoop           int
	filesToDeletePerLoop   int64
	otelCollectorEndpoint  string
}

var (
	ErrUsage = errors.New("usage: clean-nfs-cache <path> [<options>]")
	ErrFail  = errors.New("clean-nfs-cache failed to find enough space")
)

func parseArgs(args []string) (string, opts, error) {
	flags := flag.NewFlagSet("clean-nfs-cache", flag.ExitOnError)

	var opts opts
	flags.Float64Var(&opts.targetDiskUsagePercent, "disk-usage-target-percent", 90, "disk usage target as a % (0-100)")
	flags.BoolVar(&opts.dryRun, "dry-run", true, "dry run")
	flags.IntVar(&opts.filesPerLoop, "files-per-loop", 10000, "number of files to gather metadata for per loop")
	flags.Int64Var(&opts.filesToDeletePerLoop, "deletions-per-loop", 100, "maximum number of files to delete per loop")
	flags.StringVar(&opts.otelCollectorEndpoint, "otel-collector-endpoint", "", "endpoint of the otel collector")

	args = args[1:] // skip the command name
	if err := flags.Parse(args); err != nil {
		return "", opts, fmt.Errorf("could not parse flags: %w", err)
	}

	args = flags.Args()
	if len(args) != 1 {
		return "", opts, ErrUsage
	}

	return args[0], opts, nil
}

func timeit(ctx context.Context, message string, fn func()) {
	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	logger.L().Debug(ctx, message, zap.Duration("duration", done))
}
