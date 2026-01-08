package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log"
	"maps"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultChunkSize = 4 * 1024 * 1024

func main() {
	ctx := context.Background()

	var p processor
	var csvPath string

	f := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	f.BoolVar(&p.nfsStat, "nfs-stat", false, "measure nfs stat")
	f.BoolVar(&p.dropNfsCache, "drop-nfs-cache", true, "drop nfs cache")
	f.IntVar(&p.bufferSize, "buffer-size", defaultChunkSize, "size of buffer to read files into")
	f.IntVar(&p.maxFileCount, "count", 100, "number of files to read")
	f.IntVar(&p.onlyFileSize, "only-file-size", defaultChunkSize, "only read files of this size (in bytes)")
	f.IntVar(&p.concurrentRequests, "concurrent-requests", 1, "number of concurrent requests to make")
	f.StringVar(&csvPath, "csv-path", "output.csv", "path to output csv file")
	f.StringVar(&p.nfsStatFile, "nfs-stat-file", "/tmp/nfs-stat.txt", "file to store nfs stat")
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
			// "128kb": &setReadAhead{readAhead: "128"}, // always bad
			"4mb": &setReadAhead{readAhead: "4096"},
		},
		"net.core.rmem_max": {
			"208kb (default)": &setSysFs{path: "net.core.rmem_max", newValue: "212992"},
			"32mb":            &setSysFs{path: "net.core.rmem_max", newValue: "33554432"},
		},
		"net.ipv4.tcp_rmem": {
			"4 kb / 128 kb / 6 mb (default)": &setSysFs{path: "net.ipv4.tcp_rmem", newValue: "4096 131072 6291456"},
			"4 kb / 256 kb / 32 mb":          &setSysFs{path: "net.ipv4.tcp_rmem", newValue: "4096 262144 33554432"},
		},
		"sunrpc.tcp_slot_table_entries": {
			"2 (default)": &setSysFs{path: "sunrpc.tcp_slot_table_entries", newValue: "2"},
			"128":         &setSysFs{path: "sunrpc.tcp_slot_table_entries", newValue: "128"},
		},
	}

	var results []result

	for scenario := range generateScenarios(experiments) {
		fmt.Printf("\n=== Scenario: %s ===\n", scenario.Name())

		results = append(results, p.run(ctx, scenario))
	}

	if csvPath != "" {
		if err := dumpResultsToCSV(csvPath, results); err != nil {
			log.Fatalf("failed to dump results to %q: %s", csvPath, err.Error())
		}
	}
}

func dumpResultsToCSV(path string, results []result) error {
	// 1. Identify all experiment keys
	experimentKeysMap := make(map[string]struct{})
	for _, res := range results {
		for k := range res.scenario {
			experimentKeysMap[k] = struct{}{}
		}
	}
	experimentKeys := make([]string, 0, len(experimentKeysMap))
	for k := range experimentKeysMap {
		experimentKeys = append(experimentKeys, k)
	}
	slices.Sort(experimentKeys)

	// 2. Identify all metrics
	metrics := []string{"count", "min", "p50", "p95", "p99", "max", "stddev"}

	// 3. Open output.csv
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer safeClose(f)

	// 4. Write header
	header := append(slices.Clone(experimentKeys), metrics...)
	if _, err := fmt.Fprintln(f, strings.Join(header, ",")); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// 5. Write data rows
	for _, res := range results {
		row := make([]string, 0, len(header))

		// Experiment values
		for _, k := range experimentKeys {
			val := ""
			if e, ok := res.scenario[k]; ok {
				val = e.name
			}
			row = append(row, val)
		}

		// Metric values
		s := res.summary
		row = append(row,
			fmt.Sprintf("%d", s.count),
			toMillis(s.minTime),
			toMillis(s.p50),
			toMillis(s.p95),
			toMillis(s.p99),
			toMillis(s.maxTime),
			toMillis(s.stddev),
		)

		if _, err := fmt.Fprintln(f, strings.Join(row, ",")); err != nil {
			return fmt.Errorf("failed to write row: %w", err)
		}
	}

	fmt.Println("\nResults dumped to output.csv")

	return nil
}

func toMillis(minTime time.Duration) string {
	return strconv.FormatInt(minTime.Milliseconds(), 10)
}

type result struct {
	scenario scenario
	summary  durationSummary
}

func generateScenarios(experiments map[string]map[string]experiment) iter.Seq[scenario] {
	return func(yield func(scenario) bool) {
		keys := make([]string, 0, len(experiments))
		for k := range experiments {
			keys = append(keys, k)
		}

		slices.Sort(keys)

		var generate func(int, scenario) bool
		generate = func(index int, current scenario) bool {
			if index == len(keys) {
				// Create a copy of the scenario to yield
				s := make(scenario, len(current))
				maps.Copy(s, current)

				return yield(s)
			}

			key := keys[index]
			options := experiments[key]

			optionNames := make([]string, 0, len(options))
			for name := range options {
				optionNames = append(optionNames, name)
			}
			slices.Sort(optionNames)

			for _, name := range optionNames {
				current[key] = element{name: name, exp: options[name]}
				if !generate(index+1, current) {
					return false
				}
			}

			return true
		}

		generate(0, make(scenario, len(keys)))
	}
}

type setReadAhead struct {
	readAhead string

	readAheadPath string
	oldReadAhead  string
}

func (s *setReadAhead) setup(ctx context.Context, p *processor) error {
	// find nfs device
	output, err := exec.CommandContext(ctx, "findmnt", "--noheadings", "--output", "target", "--target", p.path).Output()
	if err != nil {
		return fmt.Errorf("failed to find nfs mount point: %w", err)
	}
	nfsMountPoint := strings.TrimSpace(string(output))

	// find major:minor of device
	majorMinor, err := exec.CommandContext(ctx, "mountpoint", "--fs-devno", nfsMountPoint).Output()
	if err != nil {
		return fmt.Errorf("failed to find nfs major:minor: %w", err)
	}
	s.readAheadPath = fmt.Sprintf("/sys/class/bdi/%s/read_ahead_kb", strings.TrimSpace(string(majorMinor)))

	// read old read_ahead_kb, store
	output, err = os.ReadFile(s.readAheadPath)
	if err != nil {
		return fmt.Errorf("failed to read read_ahead_kb: %w", err)
	}

	s.oldReadAhead = strings.TrimSpace(string(output))

	// set new value
	if err := os.WriteFile(s.readAheadPath, []byte(s.readAhead), 0o644); err != nil {
		return fmt.Errorf("failed to write to %q: %w", s.readAheadPath, err)
	}

	return nil
}

func (s *setReadAhead) teardown(_ context.Context, _ *processor) error {
	// reset old value
	return os.WriteFile(s.readAheadPath, []byte(s.oldReadAhead), 0o644)
}

var _ experiment = (*setReadAhead)(nil)

type setSysFs struct {
	path     string
	newValue string
	oldValue string
}

var _ experiment = (*setSysFs)(nil)

func (d *setSysFs) setup(ctx context.Context, _ *processor) error {
	// read old value
	output, err := exec.CommandContext(ctx, "sysctl", "-n", d.path).Output()
	if err != nil {
		return fmt.Errorf("failed to read sysfs value: %w", err)
	}
	d.oldValue = strings.TrimSpace(string(output))

	// set new value
	if err := exec.CommandContext(ctx, "sysctl", "-w", fmt.Sprintf("%s=%s", d.path, d.newValue)).Run(); err != nil {
		return fmt.Errorf("failed to set sysfs value: %w", err)
	}

	return nil
}

func (d *setSysFs) teardown(ctx context.Context, _ *processor) error {
	// set old value
	if err := exec.CommandContext(ctx, "sysctl", "-w", fmt.Sprintf("%s=%s", d.path, d.oldValue)).Run(); err != nil {
		return fmt.Errorf("failed to set sysfs value: %w", err)
	}

	return nil
}

type readMethodExperiment struct {
	readMethod func(string) (int, error)
}

func (r readMethodExperiment) setup(_ context.Context, p *processor) error {
	p.readMethod = r.readMethod

	return nil
}

func (r readMethodExperiment) teardown(_ context.Context, _ *processor) error {
	return nil
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
	setup(ctx context.Context, p *processor) error
	teardown(ctx context.Context, p *processor) error
}

type element struct {
	name string
	exp  experiment
}
type scenario map[string]element

func (s scenario) setup(ctx context.Context, p *processor) error {
	var errs []error

	for _, e := range s {
		if err := e.exp.setup(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("failed to setup %q: %w", e, err))
		}
	}

	return errors.Join(errs...)
}

func (s scenario) teardown(ctx context.Context, p *processor) error {
	var errs []error

	for name, e := range s {
		if err := e.exp.teardown(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("failed to teardown %q: %w", name, err))
		}
	}

	return errors.Join(errs...)
}

func (s scenario) Name() any {
	var keys []string
	for k := range s {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var values []string
	for _, k := range keys {
		values = append(values, fmt.Sprintf("%s=%s", k, s[k].name))
	}

	return strings.Join(values, "; ")
}

func (p *processor) run(ctx context.Context, scenario scenario) result {
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

	// logger.Println("setting up experiments ... ")
	if err := scenario.setup(ctx, p); err != nil {
		logger.Fatal("failed to setup experiments", "error", err)
	}

	// open/read files into buffer
	// logger.Println("starting reads ... ")
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

		semaphore <- struct{}{}

		wg.Go(func() {
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
		})
	}

	wg.Wait()

	if err := scenario.teardown(ctx, p); err != nil {
		logger.Fatal("failed to teardown experiments", "error", err)
	}

	// render times
	readSummary := summarizeDurations(reads)
	printDurationSummary("reads", readSummary)

	if p.nfsStat {
		if err := p.compareNfsStat(ctx); err != nil {
			logger.Fatal("failed to store nfs stat", "error", err)
		}
	}

	return result{
		scenario: scenario,
		summary:  readSummary,
	}
}

func printDurationSummary(label string, s durationSummary) {
	fmt.Printf(`
   %s: p50 / p95 / p99 (stddev): %s / %s / %s (%s)
`, label, s.p50, s.p95, s.p99, s.stddev)
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
