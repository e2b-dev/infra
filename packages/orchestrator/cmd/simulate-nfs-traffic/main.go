package main

import (
	"context"
	"flag"
	"fmt"
	"iter"
	"log"
	"maps"
	"math"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

const expectedFileSize = 4 * 1024 * 1024

const implausibleTime = time.Millisecond * 4

func main() {
	ctx := context.Background()

	var p processor
	var csvPath string
	var repeat int
	var pprofAddr string
	var filestoreName, filestoreZone string

	f := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	f.BoolVar(&p.nfsStat, "nfs-stat", false, "measure nfs stat")
	f.BoolVar(&p.dropNfsCache, "drop-nfs-cache", true, "drop nfs cache")
	f.IntVar(&p.limitFileCount, "limit-file-count", 10000, "limit number of files to read (0 = no limit)")
	f.StringVar(&filestoreName, "filestore-name", "", "add filestore metadata to csv")
	f.StringVar(&filestoreZone, "filestore-zone", "", "add filestore metadata to csv")
	f.StringVar(&csvPath, "csv-path", "output.csv", "path to output csv file")
	f.StringVar(&p.nfsStatFile, "nfs-stat-file", "/tmp/nfs-stat.txt", "file to store nfs stat")
	f.IntVar(&repeat, "repeat", 1, "number of times to repeat each test")
	f.StringVar(&pprofAddr, "pprof", "", "address to listen on for pprof (e.g. localhost:6060)")

	_ = f.Parse(os.Args[1:])

	if pprofAddr != "" {
		go func() {
			log.Printf("starting pprof on %s", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Printf("failed to start pprof: %s", err)
			}
		}()
	}

	switch paths := f.Args(); len(paths) {
	case 0:
		p.path = "."
	case 1:
		p.path = paths[0]
	default:
		log.Fatal("only one path can be specified")

		return
	}

	fmt.Println("getting filestore metadata ... ")
	environmentMetadata, err := getEnvironmentMetadata(ctx, filestoreName, filestoreZone)
	if err != nil {
		log.Fatalf("failed to get filestore metadata for %q: %s", filestoreName, err)
	}

	var results []result

	p.readCount = 100

	fmt.Println("generating files ... ")
	if err := p.findFiles(); err != nil {
		log.Fatalf("failed to generate files: %s", err)
	}

	totalScenarios := 1
	for _, options := range experiments {
		totalScenarios *= len(options)
	}
	totalScenarios *= repeat

	currentScenario := 0
	for scenario := range generateScenarios(experiments) {
		for i := range repeat {
			currentScenario++

			result, err := p.run(ctx, scenario)
			if err != nil {
				log.Fatalf("failed to run scenario: %s", err.Error())
			}

			limit := fmt.Sprintf("%d reads", result.totalSuccessfulReads)
			if repeat > 1 {
				fmt.Printf("\n=== Scenario %d/%d [%s]: %s (run %d/%d) ===\n", currentScenario, totalScenarios, limit, scenario.Name(), i+1, repeat)
			} else {
				fmt.Printf("\n=== Scenario %d/%d [%s]: %s ===\n", currentScenario, totalScenarios, limit, scenario.Name())
			}

			results = append(results, result)
		}
	}

	if csvPath != "" {
		if err := dumpResultsToCSV(csvPath, environmentMetadata, results); err != nil {
			log.Fatalf("failed to dump results to %q: %s", csvPath, err.Error())
		}
	}
}

func mustParseInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}

	return n
}

type result struct {
	scenario scenario
	summary  durationSummary

	concurrency          int
	totalSuccessfulReads int
	testDuration         time.Duration
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
				var s scenario
				s.elements = make(map[string]element, len(current.elements))
				maps.Copy(s.elements, current.elements)

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
				current.elements[key] = element{name: name, exp: options[name]}
				if !generate(index+1, current) {
					return false
				}
			}

			return true
		}

		var s scenario
		s.elements = make(map[string]element, len(keys))
		generate(0, s)
	}
}

type processor struct {
	path               string
	dropNfsCache       bool
	nfsStat            bool
	readMethod         func(string) (int, error)
	concurrentRequests int
	readCount          int
	skipCount          int
	allowRepeatReads   bool
	limitFileCount     int

	allFiles    []string
	nfsStatFile string
}

func (p *processor) run(ctx context.Context, scenario scenario) (result, error) {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	if p.dropNfsCache {
		if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3"), 0o644); err != nil {
			return result{}, fmt.Errorf("failed to drop nfs cache: %w", err)
		}
	}

	if p.nfsStat {
		if err := p.storeNfsStat(); err != nil {
			return result{}, fmt.Errorf("failed to store nfs stat: %w", err)
		}
	}

	// logger.Println("setting up experiments ... ")
	if err := scenario.setup(ctx, p); err != nil {
		return result{}, fmt.Errorf("failed to setup experiments: %w", err)
	}

	// open/read files into buffer
	// logger.Println("starting reads ... ")
	var reads []time.Duration
	var sizes []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, p.concurrentRequests)

	testStart := time.Now()
	testCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	allFiles := &files{
		rand:             rand.New(rand.NewSource(time.Now().UnixNano())),
		paths:            slices.Clone(p.allFiles),
		allowRepeatReads: p.allowRepeatReads,
	}

	for i := 0; i < p.readCount && testCtx.Err() == nil; i++ {
		f, err := allFiles.selectFile()
		if err != nil {
			logger.Println("failed to get file", "error", err)

			break
		}

		semaphore <- struct{}{}

		iteration := i
		wg.Add(1)
		go func() {
			defer func() { <-semaphore }()
			defer wg.Done()

			start := time.Now()

			size, err := p.readMethod(f)
			if err != nil {
				logger.Println("failed to time file read", "error", err)

				return
			}

			duration := time.Since(start)

			if testCtx.Err() != nil {
				return
			}

			if iteration < p.skipCount {
				return
			}

			mu.Lock()
			defer mu.Unlock()

			sizes = append(sizes, size)
			reads = append(reads, duration)

			if duration < implausibleTime {
				fmt.Printf("!! read %d bytes from %s in %s\n", size, f, duration.Round(time.Millisecond))
			}
		}()
	}

	wg.Wait()

	if err := scenario.teardown(ctx, p); err != nil {
		return result{}, fmt.Errorf("failed to teardown experiments: %w", err)
	}

	// render times
	readSummary := summarizeDurations(reads)

	if p.nfsStat {
		if err := p.compareNfsStat(ctx); err != nil {
			return result{}, fmt.Errorf("failed to compare nfs stat: %w", err)
		}
	}

	return result{
		scenario:             scenario,
		summary:              readSummary,
		concurrency:          p.concurrentRequests,
		totalSuccessfulReads: len(reads),
		testDuration:         time.Since(testStart),
	}, nil
}

func safeClose(fp *os.File) {
	if err := fp.Close(); err != nil {
		log.Println("failed to close file", "error", err)
	}
}

type durationSummary struct {
	minTime, mean, p50, p95, p99, maxTime, stddev time.Duration
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
		minTime: reads[0],
		maxTime: reads[n-1],
		mean:    time.Duration(mean),
		p50:     percentile(50),
		p95:     percentile(95),
		p99:     percentile(99),
		stddev:  time.Duration(stdDev),
	}
}
