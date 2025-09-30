package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/pkg"
)

func main() {
	if err := cleanNFSCache(context.Background()); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func cleanNFSCache(ctx context.Context) error {
	path, opts, err := parseArgs()
	if err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}

	// get free space information for path
	fmt.Printf("dry run: %t\n", opts.dryRun)
	fmt.Printf("target disk usage percentage: %f%%\n", opts.targetDiskUsagePercent)

	var diskInfo pkg.DiskInfo
	timeit(fmt.Sprintf("getting disk info for %q", path), func() {
		diskInfo, err = pkg.GetDiskInfo(ctx, path)
	})
	if err != nil {
		return fmt.Errorf("could not get disk info: %w", err)
	}
	targetDiskUsage := int64(float64(opts.targetDiskUsagePercent) / 100 * float64(diskInfo.Total))
	areWeDone := func() bool {
		currentUsedPercentage := (float64(diskInfo.Used) / float64(diskInfo.Total)) * 100
		fmt.Printf("current usage: %d%% (%s)\n", int(currentUsedPercentage), formatBytes(diskInfo.Used))
		return diskInfo.Used < targetDiskUsage
	}

	cache := pkg.NewListingCache(path)

	var allResults results
	defer printSummary(allResults, opts)

	// if conditions are met, we're done
	for !areWeDone() {
		// get File metadata, including path, size, and last access timestamp
		var files []pkg.File
		timeit(fmt.Sprintf("gathering metadata on %d files", opts.filesPerLoop), func() {
			files, err = getFiles(cache, opts.filesPerLoop)
			fmt.Printf("got %d files", len(files))
		})
		if err != nil {
			return fmt.Errorf("could not get File metadata: %w", err)
		}

		// sort files by access timestamp
		timeit(fmt.Sprintf("sorting %d files by access time", len(files)), func() {
			sortFilesByATime(files)
		})

		var results results
		timeit(fmt.Sprintf("deleting bottom %d%% files\n", opts.filesToDeletePerLoop), func() {
			results, err = deleteOldestFiles(cache, files, opts, &diskInfo, areWeDone, opts.filesToDeletePerLoop)
		})
		allResults = allResults.union(results)
		if err != nil {
			return fmt.Errorf("failed to delete files: %w", err)
		}
	}

	return nil
}

func printSummary(r results, opts opts) {
	if r.deletedFiles == 0 {
		fmt.Fprintln(os.Stderr, "no files deleted")
		return
	}

	var notice string
	if opts.dryRun {
		notice = "would be "
	}
	fmt.Println("======= summary =======")
	if opts.dryRun {
		fmt.Println("(note: dry-run mode enabled, no files were actually deleted)")
	}
	fmt.Printf(" %d files (%d bytes) %sdeleted\n", r.deletedFiles, r.deletedBytes, notice)
	fmt.Println("access time:")
	fmt.Printf("- most recently used: %s\n", minDuration(r.lastAccessed).Round(time.Second))
	fmt.Printf("- least recently used: %s\n", maxDuration(r.lastAccessed).Round(time.Second))
	fmt.Printf("- standard deviation: %s\n", standardDeviation(r.lastAccessed).Round(time.Second))
}

func standardDeviation(accessed []time.Duration) time.Duration {
	var sum time.Duration
	for i := range accessed {
		sum += accessed[i]
	}
	mean := sum / time.Duration(len(accessed))

	var sd float64
	for i := range accessed {
		sd += math.Pow(float64(accessed[i]-mean), 2)
	}

	sd = math.Sqrt(sd / float64(len(accessed)))
	return time.Duration(sd)
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

func (r results) union(other results) results {
	return results{
		deletedFiles:     r.deletedFiles + other.deletedFiles,
		deletedBytes:     r.deletedBytes + other.deletedBytes,
		lastAccessed:     append(r.lastAccessed, other.lastAccessed...),
		createdDurations: append(r.createdDurations, other.createdDurations...),
	}
}

func deleteOldestFiles(cache *pkg.ListingCache, files []pkg.File, opts opts, diskInfo *pkg.DiskInfo, areWeDone func() bool, deleteCount int64) (results, error) {
	now := time.Now()
	var results results
	for index, file := range files {
		if opts.dryRun {
			fmt.Printf("DRY RUN: would delete %q (%d bytes, created: %s, last access: %s)\n",
				file.Path, file.Size,
				time.Since(file.BTime).Round(time.Second),
				time.Since(file.ATime).Round(time.Minute))
		} else {
			// remove File
			fmt.Printf("deleting #%d: %q (%d bytes) ... ", index+1, file.Path, file.Size)
			if err := os.Remove(file.Path); err != nil {
				// if we fail to delete the File, try the next one
				fmt.Printf("failed to delete %q: %v\n", file.Path, err)
				continue
			}
			fmt.Print("\n")
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

func reportGetFilesProgress(usedFiles, dupeHits int) {
	total := usedFiles + dupeHits
	if total > 0 && total%100 == 0 {
		msg := fmt.Sprintf("%d files", usedFiles)
		if dupeHits > 0 {
			msg += fmt.Sprintf(", %d dupe hits", dupeHits)
		}
		fmt.Printf("\r%s ... ", msg)
	}
}

func getFiles(cache *pkg.ListingCache, maxFiles int) ([]pkg.File, error) {
	var items []pkg.File

	usedFiles := make(map[string]struct{})
	var dupeHits int

	for len(items) != maxFiles {
		reportGetFilesProgress(len(usedFiles), dupeHits)

		path, err := cache.GetRandomFile()
		if err != nil {
			return items, err
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

	return items, nil
}

type opts struct {
	targetDiskUsagePercent float64
	dryRun                 bool
	filesPerLoop           int
	filesToDeletePerLoop   int64
}

var (
	ErrUsage = errors.New("usage: clean-nfs-cache <path> [<options>]")
	ErrFail  = errors.New("clean-nfs-cache failed to find enough space")
)

func parseArgs() (string, opts, error) {
	flags := flag.NewFlagSet("clean-nfs-cache", flag.ExitOnError)

	var opts opts
	flags.Float64Var(&opts.targetDiskUsagePercent, "disk-usage-target-percent", 10, "disk usage target as a % (0-100)")
	flags.BoolVar(&opts.dryRun, "dry-run", true, "dry run")
	flags.IntVar(&opts.filesPerLoop, "files-per-iteration", 10000, "number of files to gather metadata for per loop")
	flags.Int64Var(&opts.filesToDeletePerLoop, "max-files-per-iteration", 100, "maximum number of files to delete per loop")

	args := os.Args[1:] // skip the command name
	if err := flags.Parse(args); err != nil {
		return "", opts, fmt.Errorf("could not parse flags: %w", err)
	}

	args = flags.Args()
	if len(args) != 1 {
		return "", opts, ErrUsage
	}

	return args[0], opts, nil
}

func timeit(message string, fn func()) {
	fmt.Print(message + " ... \n")

	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	fmt.Print(message + fmt.Sprintf(": done in [%s]\n", done))
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB",
		float64(b)/float64(div), "KMGTPE"[exp])
}
