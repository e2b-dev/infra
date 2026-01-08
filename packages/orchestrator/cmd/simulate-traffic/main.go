package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
)

const defaultChunkSize = 4 * 1024 * 1024

func main() {
	ctx := context.Background()

	var p processor

	var readMethod string

	f := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	f.BoolVar(&p.nfsStat, "nfs-stat", false, "measure nfs stat")
	f.BoolVar(&p.dropNfsCache, "drop-nfs-cache", true, "drop nfs cache")
	f.IntVar(&p.bufferSize, "buffer-size", defaultChunkSize, "size of buffer to read files into")
	f.IntVar(&p.maxFileCount, "count", 100, "number of files to read")
	f.IntVar(&p.onlyFileSize, "only-file-size", defaultChunkSize, "only read files of this size (in bytes)")
	f.StringVar(&readMethod, "read-method", "ReadAt", "read method to use (ReadAt or Read)")
	_ = f.Parse(os.Args[1:])

	paths := f.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	readMethods := map[string]func(string) (int, error){
		"ReadAt":   p.readAtFile,
		"ReadFile": p.read,
	}

	var ok bool
	if p.readMethod, ok = readMethods[readMethod]; !ok {
		log.Fatalf("invalid read method: %s", readMethod)
	}

	p.run(ctx, paths)
}

type processor struct {
	dropNfsCache bool
	nfsStat      bool
	maxFileCount int
	bufferSize   int
	onlyFileSize int
	readMethod   func(string) (int, error)

	nfsStatFile string
}

func (p *processor) run(ctx context.Context, paths []string) {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	if p.dropNfsCache {
		if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3"), 0o644); err != nil {
			logger.Fatal("failed to drop nfs cache", "error", err)
		}
	}

	if p.nfsStat {
		if err := p.storeNfsStat(); err != nil {
			logger.Fatal("failed to store nfs stat", "error", err)
		}
	}

	allFiles := files{
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for _, path := range paths {
		// find 4 MB files under $path
		paths, err := p.findFiles(path)
		if err != nil {
			logger.Fatal("failed to find files", "error", err)
		}

		allFiles.addPaths(paths)
	}

	// open/read files into buffer
	var reads []time.Duration
	var sizes []int
	for range p.maxFileCount {
		f, err := allFiles.selectFile()
		if err != nil {
			logger.Println("failed to get file", "error", err)

			break
		}

		start := time.Now()

		size, err := p.readMethod(f)
		if err != nil {
			logger.Println("failed to time file read", "error", err)

			break
		}

		duration := time.Since(start)
		sizes = append(sizes, size)
		reads = append(reads, duration)
	}

	// render times
	readSummary := summarizeDurations(reads)
	printDurationSummary("reads", readSummary)

	sizeSummary := summarizeBytes(sizes)
	printByteSummary("sizes", sizeSummary)

	if p.nfsStat {
		if err := p.compareNfsStat(ctx); err != nil {
			logger.Fatal("failed to store nfs stat", "error", err)
		}
	}
}

type intSummary struct {
	count, min, max, stddev, p50, p95, p99 uint64
}

func summarizeBytes(ints []int) intSummary {
	if len(ints) == 0 {
		return intSummary{}
	}

	// Sort to find percentiles, min, and max
	slices.Sort(ints)

	n := len(ints)

	// Helper for percentiles
	percentile := func(p float64) uint64 {
		idx := max(int(math.Ceil(p/100*float64(n)))-1, 0)

		return uint64(ints[idx])
	}

	// Basic stats
	var sum float64
	for _, r := range ints {
		sum += float64(r)
	}
	mean := sum / float64(n)

	// Standard deviation
	var varianceSum float64
	for _, r := range ints {
		diff := float64(r) - mean
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(n))

	return intSummary{
		count:  uint64(n),
		min:    uint64(ints[0]),
		max:    uint64(ints[n-1]),
		p50:    percentile(50),
		p95:    percentile(95),
		p99:    percentile(99),
		stddev: uint64(stdDev),
	}
}

func printByteSummary(label string, s intSummary) {
	fmt.Printf(`
==== %s ====
count: %d
min: %s
p50: %s
p95: %s
p99: %s
max: %s
stddev: %s
`, label, s.count, humanize.Bytes(s.min), humanize.Bytes(s.p50), humanize.Bytes(s.p95), humanize.Bytes(s.p99), humanize.Bytes(s.max), humanize.Bytes(s.stddev))
}

func printDurationSummary(label string, s durationSummary) {
	fmt.Printf(`
==== %s ====
count: %d
min: %s
p50: %s
p95: %s
p99: %s
max: %s
stddev: %s
`, label, s.count, s.minTime, s.p50, s.p95, s.p99, s.maxTime, s.stddev)
}

type files struct {
	rand  *rand.Rand
	paths []string
}

func (p *processor) findFiles(path string) ([]string, error) {
	var paths []string

	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// If fixedSize is specified, check the file size
		if p.onlyFileSize > 0 {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Size() != int64(p.onlyFileSize) {
				return nil
			}
		}

		paths = append(paths, path)

		if len(paths) == p.maxFileCount {
			return filepath.SkipAll // we're done
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return paths, nil
}

func (f *files) selectFile() (string, error) {
	if len(f.paths) == 0 {
		return "", fmt.Errorf("no files found")
	}

	idx := rand.Intn(len(f.paths))
	path := f.paths[idx]

	// remove path from paths
	f.paths = append(f.paths[:idx], f.paths[idx+1:]...)

	return path, nil
}

func (f *files) addPaths(paths []string) {
	f.paths = append(f.paths, paths...)
}

func (p *processor) readAtFile(path string) (int, error) {
	fp, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer safeClose(fp)

	buff := make([]byte, p.bufferSize)

	return fp.ReadAt(buff, 0)
}

func (p *processor) read(path string) (int, error) {
	fp, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer safeClose(fp)

	buff := make([]byte, p.bufferSize)

	var total int
	for {
		n, err := fp.Read(buff)
		total += n

		if err != nil {
			if err == io.EOF {
				break
			}

			return total, fmt.Errorf("failed to read from file: %w", err)
		}
	}

	return total, nil
}

func (p *processor) storeNfsStat() error {
	data, err := os.ReadFile("/proc/net/rpc/nfs")
	if err != nil {
		return fmt.Errorf("failed to read nfs stat: %w", err)
	}

	fname, err := os.CreateTemp("", "nfs-stat-*.txt")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer safeClose(fname)

	if _, err := fname.Write(data); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	p.nfsStatFile = fname.Name()

	return nil
}

func (p *processor) compareNfsStat(ctx context.Context) error {
	defer safeRemove(p.nfsStatFile)

	output, err := exec.CommandContext(ctx, "nfsstat", "--list", "--since", p.nfsStatFile, "/proc/net/rpc/nfs").Output()
	if err != nil {
		return fmt.Errorf("failed to compare nfs stat: %w", err)
	}

	stats, err := nfsstatParse(string(output))
	if err != nil {
		return fmt.Errorf("failed to parse nfs stat: %w", err)
	}

	summarizeNfsstat(stats)

	return nil
}

func summarizeNfsstat(stats []nfsstat) {
	for _, stat := range stats {
		fmt.Printf("%s:\t%s:\t%d\n", stat.category, stat.function, stat.count)
	}
}

type nfsstat struct {
	category, function string
	count              int
}

func nfsstatParse(s string) ([]nfsstat, error) {
	var stats []nfsstat
	lines := strings.SplitSeq(s, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "---") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		keyPart := strings.TrimSpace(parts[0])
		valuePart := strings.TrimSpace(parts[1])

		lastSpaceIdx := strings.LastIndex(keyPart, " ")
		if lastSpaceIdx == -1 {
			continue
		}

		category := strings.TrimSpace(keyPart[:lastSpaceIdx])
		function := strings.TrimSpace(keyPart[lastSpaceIdx+1:])

		var count int
		if _, err := fmt.Sscanf(valuePart, "%d", &count); err != nil {
			return nil, fmt.Errorf("failed to parse count for %s %s: %w", category, function, err)
		}

		stats = append(stats, nfsstat{
			category: category,
			function: function,
			count:    count,
		})
	}

	return stats, nil
}

func safeRemove(file string) {
	if err := os.Remove(file); err != nil {
		log.Println("failed to remove file", "error", err)
	}
}

func safeClose(fp *os.File) {
	if err := fp.Close(); err != nil {
		log.Println("failed to close file", "error", err)
	}
}

type durationSummary struct {
	count                                   int
	minTime, p50, p95, p99, maxTime, stddev time.Duration
}

func summarizeDurations(reads []time.Duration) durationSummary {
	if len(reads) == 0 {
		return durationSummary{}
	}

	// Sort to find percentiles, min, and max
	slices.Sort(reads)

	n := len(reads)

	// Helper for percentiles
	percentile := func(p float64) time.Duration {
		idx := max(int(math.Ceil(p/100*float64(n)))-1, 0)

		return reads[idx]
	}

	// Basic stats
	var sum float64
	for _, r := range reads {
		sum += float64(r)
	}
	mean := sum / float64(n)

	// Standard deviation
	var varianceSum float64
	for _, r := range reads {
		diff := float64(r) - mean
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(n))

	return durationSummary{
		count:   n,
		minTime: reads[0],
		maxTime: reads[n-1],
		p50:     percentile(50),
		p95:     percentile(95),
		p99:     percentile(99),
		stddev:  time.Duration(stdDev),
	}
}
