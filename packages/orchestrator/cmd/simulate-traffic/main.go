package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
)

const defaultChunkSize = 4 * 1024 * 1024

func main() {
	ctx := context.Background()

	var p processor

	f := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	f.BoolVar(&p.nfsStat, "nfs-stat", false, "measure nfs stat")
	f.BoolVar(&p.dropNfsCache, "drop-nfs-cache", true, "drop nfs cache")
	f.IntVar(&p.bufferSize, "buffer-size", defaultChunkSize, "size of buffer to read files into")
	f.IntVar(&p.maxFileCount, "count", 100, "number of files to read")
	f.IntVar(&p.onlyFileSize, "only-file-size", defaultChunkSize, "only read files of this size (in bytes)")
	f.IntVar(&p.concurrentRequests, "concurrent-requests", 1, "number of concurrent requests to make")
	_ = f.Parse(os.Args[1:])

	switch paths := f.Args(); len(paths) {
	case 0:
		p.path = "."
	case 1:
		p.path = paths[0]
	default:
		log.Fatal("only one path can be specified")
		return
	}

	experiments := map[string]map[string]experiment{
		"read method": {
			"ReadAt": readMethodExperiment{p.readAtFile},
			"Read":   readMethodExperiment{p.read},
		},
		"nfs read ahead": {
			"128kb": &setReadAhead{readAhead: 128},
			"4mb":   &setReadAhead{readAhead: 4096},
		},
		"net.core.rmem_max": {
			"default": defaultSysFs{"net.core.rmem_max"},
			"32mb":    &setSysFs{path: "net.core.rmem_max", newValue: "33554432"},
		},
		"net.ipv4.tcp_rmem": {
			"default":           defaultSysFs{"net.ipv4.tcp_rmem"},
			"4k / 256k / 32 mb": &setSysFs{path: "net.ipv4.tcp_rmem", newValue: "4096 262144 33554432"},
		},
		"sunrpc.tcp_slot_table_entries": {
			"default": defaultSysFs{"sunrpc.tcp_slot_table_entries"},
			"128":     &setSysFs{path: "sunrpc.tcp_slot_table_entries", newValue: "128"},
		},
	}

	for scenario := range generateScenarios(experiments) {
		p.run(ctx, scenario)
	}
}

func generateScenarios(experiments map[string]map[string]experiment) iter.Seq[scenario] {
	return func(yield func(scenario) bool) {

	}
}

type setReadAhead struct {
	readAhead int

	readAheadPath string
	oldReadAhead  int
}

func (s *setReadAhead) setup(p *processor) {
	// find nfs device

	// read old read_ahead_kb, store

	// set new value

	panic("implement me")
}

func (s *setReadAhead) teardown(p *processor) {
	// reset old value
	panic("implement me")
}

var _ experiment = (*setReadAhead)(nil)

type setSysFs struct {
	path     string
	newValue string
	oldValue string
}

var _ experiment = (*setSysFs)(nil)

func (d *setSysFs) setup(p *processor) {
	// read old value

	// set new value

	panic("implement me")
}

func (d *setSysFs) teardown(p *processor) {
	// set old value
	panic("implement me")
}

type defaultSysFs struct {
	path string
}

func (d defaultSysFs) setup(p *processor) {
	// report current sysfs value
}

func (d defaultSysFs) teardown(p *processor) {
}

var _ experiment = (*defaultSysFs)(nil)

type readMethodExperiment struct {
	readMethod func(string) (int, error)
}

func (r readMethodExperiment) setup(p *processor) {
	p.readMethod = r.readMethod
}

func (r readMethodExperiment) teardown(p *processor) {
}

var _ experiment = (*readMethodExperiment)(nil)

type processor struct {
	path               string
	dropNfsCache       bool
	nfsStat            bool
	maxFileCount       int
	bufferSize         int
	onlyFileSize       int
	readMethod         func(string) (int, error)
	concurrentRequests int

	nfsStatFile string
}

type experiment interface {
	setup(p *processor)
	teardown(p *processor)
}

type scenario map[string]experiment

func (s scenario) setup(p *processor) {
	for _, exp := range s {
		exp.setup(p)
	}
}

func (s scenario) teardown(p *processor) {
	for _, exp := range s {
		exp.teardown(p)
	}
}

func (p *processor) run(ctx context.Context, scenario scenario) {
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
	// find 4 MB files under $path
	paths, err := p.findFiles(p.path)
	if err != nil {
		logger.Fatal("failed to find files", "error", err)
	}
	allFiles.addPaths(paths)

	logger.Println("setting up experiments ... ")
	scenario.setup(p)

	// open/read files into buffer
	logger.Println("starting reads ... ")
	var reads []time.Duration
	var sizes []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, p.concurrentRequests)

	for range p.maxFileCount {
		f, err := allFiles.selectFile()
		if err != nil {
			logger.Println("failed to get file", "error", err)

			break
		}

		wg.Add(1)
		semaphore <- struct{}{}

		go func(f string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			start := time.Now()

			size, err := p.readMethod(f)
			if err != nil {
				logger.Println("failed to time file read", "error", err)

				return
			}

			duration := time.Since(start)

			mu.Lock()
			defer mu.Unlock()

			sizes = append(sizes, size)
			reads = append(reads, duration)
		}(f)
	}

	wg.Wait()

	logger.Println("tearing down experiments ... ")
	scenario.teardown(p)

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
