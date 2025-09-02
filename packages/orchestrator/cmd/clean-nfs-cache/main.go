package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := cleanNFSCache(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func cleanNFSCache() error {
	path, opts, err := parseArgs()
	if err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}

	// get free space information for path

	var diskInfo diskInfo
	timeit(fmt.Sprintf("getting disk info for %q ... ", path), func() {
		diskInfo, err = getDiskInfo(path)
	})
	if err != nil {
		return fmt.Errorf("could not get disk info: %w", err)
	}
	targetDiskUsage := int64(float64(opts.targetDiskUsagePercent) / 100 * float64(diskInfo.total))
	areWeDone := func() bool {
		fmt.Printf("testing %d < %d", diskInfo.used, targetDiskUsage)
		return diskInfo.used < targetDiskUsage
	}

	// if conditions are met, we're done
	currentUsedPercentage := (float64(diskInfo.used) / float64(diskInfo.total)) * 100
	if areWeDone() {
		fmt.Printf("condition already met (disk usage target: %.2f%% > actual: %.2f%%)\n",
			opts.targetDiskUsagePercent, currentUsedPercentage)
		return nil
	}

	fmt.Printf("disk is currently %.2f%% full, target is: %.2f%%\n",
		currentUsedPercentage, opts.targetDiskUsagePercent)

	// get file metadata, including path, size, and last access timestamp
	var files []file
	timeit("gathering metadata ... ", func() {
		files, err = getFileMetadata(path)
	})
	if err != nil {
		return fmt.Errorf("could not get file metadata: %w", err)
	}

	// sort files by access timestamp
	timeit(fmt.Sprintf("sorting %d files by access time ...", len(files)), func() {
		sortFiles(files)
	})

	var results results
	err = nil
	timeit(fmt.Sprintf("looking through %d files for deletion candidates ... \n", len(files)), func() {
		results, err = deleteFiles(files, opts, &diskInfo, areWeDone)
	})

	printSummary(results, opts)

	return err
}

func printSummary(r results, opts opts) {
	fmt.Println("======= summary of deleted files =======")
	if opts.dryRun {
		fmt.Println("(note: dry-run mode enabled, no files were actually deleted)")
	}
	fmt.Printf(" %d files (%d bytes) deleted\n", r.deletedFiles, r.deletedBytes)
	fmt.Println("access time:")
	fmt.Printf("- most recently used: %s\n", minDuration(r.lastAccessed).Round(time.Second))
	fmt.Printf("- least recently used: %s\n", maxDuration(r.lastAccessed).Round(time.Second))
	fmt.Printf("- standard deviation: %s\n", standardDeviation(r.lastAccessed).Round(time.Second))
	fmt.Println("creation time:")
	fmt.Printf("- oldest file: %s\n", minDuration(r.createdDurations).Round(time.Second))
	fmt.Printf("- newest file: %s\n", maxDuration(r.createdDurations).Round(time.Second))
	fmt.Printf("- standard deviation: %s\n", standardDeviation(r.createdDurations).Round(time.Second))
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

func deleteFiles(files []file, opts opts, diskInfo *diskInfo, areWeDone func() bool) (results, error) {
	now := time.Now()
	var results results
	for _, file := range files {
		if opts.dryRun {
			fmt.Printf("DRY RUN: would delete %q (%d bytes, created: %s, last access: %s)\n",
				file.path, file.size,
				time.Since(file.btime).Round(time.Second),
				time.Since(file.atime).Round(time.Minute))
		} else {
			// remove file
			fmt.Printf("deleting %q (%d bytes) ... ", file.path, file.size)
			if err := os.Remove(file.path); err != nil {
				// if we fail to delete the file, try the next one
				fmt.Printf("failed to delete %q: %v\n", file.path, err)
				continue
			}
			fmt.Print("\n")
		}

		results.deletedFiles++
		results.deletedBytes += file.size
		results.lastAccessed = append(results.lastAccessed, now.Sub(file.atime))
		results.createdDurations = append(results.createdDurations, time.Since(file.btime))

		// record the file as free space
		diskInfo.used -= file.size
		if areWeDone() {
			// we're done!
			return results, nil
		}
	}

	return results, fmt.Errorf("%w: target: %.2f%% < actual: %.2f%%",
		ErrFail, opts.targetDiskUsagePercent,
		(float64(diskInfo.used)/float64(diskInfo.total))*100)
}

type diskInfo struct {
	total, used int64
}

func getDiskInfo(path string) (diskInfo, error) {
	// Execute: df <path>
	cmd := exec.Command("df", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return diskInfo{}, fmt.Errorf("df command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return diskInfo{}, fmt.Errorf("unexpected df output: %q", strings.TrimSpace(string(out)))
	}

	// Skip header (line 0) and parse the first data line
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		totalSize, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return diskInfo{}, fmt.Errorf("failed to parse total size: %w", err)
		}

		usedSpace, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return diskInfo{}, fmt.Errorf("failed to parse available space: %w", err)
		}

		// "df" returns kilobytes, not bytes
		return diskInfo{total: totalSize * 1024, used: usedSpace * 1024}, nil
	}

	return diskInfo{}, fmt.Errorf("could not parse mount point from df output: %q", strings.TrimSpace(string(out)))
}

func sortFiles(files []file) {
	sort.Slice(files, func(i, j int) bool {
		return files[j].atime.After(files[i].atime)
	})
}

func getFileMetadata(path string) ([]file, error) {
	var items []file

	if err := filepath.WalkDir(path, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("could not walk dir %q: %w", path, err)
		}

		if info.IsDir() {
			return nil
		}

		item, err := getMetadata(path)
		if err != nil {
			return fmt.Errorf("could not get metadata: %w", err)
		}

		items = append(items, item)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}

	return items, nil
}

type opts struct {
	targetDiskUsagePercent float64
	dryRun                 bool
}

type file struct {
	path         string
	size         int64
	atime, btime time.Time
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

	if err := flags.Parse(os.Args[1:]); err != nil {
		return "", opts, fmt.Errorf("could not parse flags: %w", err)
	}

	args := flags.Args()
	if len(args) != 1 {
		return "", opts, ErrUsage
	}

	return args[0], opts, nil
}

func timeit(message string, fn func()) {
	fmt.Print(message)

	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	fmt.Printf("done in [%s]\n", done)
}
