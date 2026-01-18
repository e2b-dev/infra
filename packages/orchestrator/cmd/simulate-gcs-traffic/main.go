package main

import (
	"context"
	"flag"
	"fmt"
	"iter"
	"log"
	"maps"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"slices"
	"sync"
	"time"
)

const (
	kilobyte        = 1024
	megabyte        = 1024 * kilobyte
	gigabyte        = 1024 * megabyte
	implausibleTime = time.Millisecond * 4
)

var histogramDurations = []time.Duration{
	25 * time.Millisecond,
	50 * time.Millisecond,
	75 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	2500 * time.Millisecond,
}

func main() {
	ctx := context.Background()

	var p processor
	var csvPath string
	var repeat int
	var gatherMetadata bool
	var pprofAddr string
	var allOptionsToCSV bool

	f := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	f.BoolVar(&allOptionsToCSV, "all-options-to-csv", false, "write all options to csv")
	f.BoolVar(&gatherMetadata, "gather-metadata", true, "gather metadata about files")
	f.IntVar(&p.limitFileCount, "limit-file-count", 10000, "limit number of files to read (0 = no limit)")
	f.IntVar(&repeat, "repeat", 1, "number of times to repeat each test")
	f.Int64Var(&p.minFileSize, "min-file-size", 100*megabyte, "number of concurrent requests")
	f.StringVar(&csvPath, "csv-path", "output.csv", "path to output csv file")
	f.StringVar(&p.bucketPrefix, "bucket-prefix", "", "prefix to filter objects by")
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
		log.Fatal("bucket must be specified")
	case 1:
		p.bucket = paths[0]
	default:
		log.Fatal("only one bucket can be specified")

		return
	}

	var err error
	var metadata environmentMetadata
	if gatherMetadata {
		fmt.Println("getting metadata ... ")
		metadata, err = getEnvironmentMetadata(ctx)
		if err != nil {
			log.Fatalf("failed to get metadata: %s", err)
		}
	} else {
		metadata = environmentMetadata{
			ClientMachineType: "disabled",
		}
	}

	var results []result

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

			if repeat > 1 {
				fmt.Printf("\n=== Scenario %d/%d [%s] (run %d/%d) ===\n", currentScenario, totalScenarios, scenario.Name(), i+1, repeat)
			} else {
				fmt.Printf("\n=== Scenario %d/%d [%s] ===\n", currentScenario, totalScenarios, scenario.Name())
			}

			result, err := p.run(ctx, scenario)
			if err != nil {
				log.Fatalf("failed to run scenario: %s", err.Error())
			}

			results = append(results, result)
		}
	}

	if csvPath != "" {
		if err := dumpResultsToCSV(experiments, csvPath, metadata, results, allOptionsToCSV); err != nil {
			log.Fatalf("failed to dump results to %q: %s", csvPath, err.Error())
		}
	}
}

type fileInfo struct {
	path string
	size int64
}

type readInfo struct {
	path   string
	offset int64
	buffer []byte
}

type (
	readMethod   func(context.Context, readInfo) (time.Duration, error)
	bufferMethod func() []byte
)

type processor struct {
	// configuration
	minFileSize    int64
	bucket         string
	bucketPrefix   string
	limitFileCount int

	// state
	allFiles []fileInfo
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

func (p *processor) run(ctx context.Context, scenario scenario) (result, error) {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	// logger.Println("setting up experiments ... ")
	opts, err := scenario.setup(ctx, p)
	if err != nil {
		return result{}, fmt.Errorf("failed to setup experiments: %w", err)
	}

	// open/read files into buffer
	// logger.Println("starting reads ... ")
	var reads []time.Duration
	var mu sync.Mutex
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, opts.concurrentRequests)

	testStart := time.Now()

	allFiles := newFiles(slices.Clone(p.allFiles), opts.chunkSize, opts.allowRepeatReads)

	for i := range opts.readCount {
		path, offset, err := allFiles.nextRead()
		if err != nil {
			logger.Printf("failed to get file: %v", err)

			break
		}

		semaphore <- struct{}{}

		wg.Go(func() {
			defer func() { <-semaphore }()

			readInfo := readInfo{
				path:   path,
				offset: offset,
				buffer: opts.makeBuffer(),
			}
			duration, err := opts.readMethod(ctx, readInfo)
			if err != nil {
				logger.Println("failed to time file read", "error", err)

				return
			}

			if i < opts.skipCount {
				return
			}

			mu.Lock()
			defer mu.Unlock()

			reads = append(reads, duration)

			if duration < implausibleTime {
				fmt.Printf("!! read from %s in %s\n", path, duration.Round(time.Millisecond))
			}
		})
	}

	wg.Wait()

	if err := scenario.teardown(ctx, opts); err != nil {
		return result{}, fmt.Errorf("failed to teardown experiments: %w", err)
	}

	// render times
	readSummary := summarizeDurations(reads)

	return result{
		scenario:             scenario,
		summary:              readSummary,
		concurrency:          opts.concurrentRequests,
		totalSuccessfulReads: len(reads),
		testDuration:         time.Since(testStart),
	}, nil
}

type result struct {
	scenario scenario
	summary  durationSummary

	concurrency          int
	totalSuccessfulReads int
	testDuration         time.Duration
}

func safeClose(fp interface{ Close() error }) {
	if err := fp.Close(); err != nil {
		log.Println("close failed", "error", err)
	}
}

type durationSummary struct {
	minTime, mean, p50, p95, p99, maxTime, stddev time.Duration
	histogram                                     []int
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
	histogram := make([]int, len(histogramDurations))
	for _, r := range reads {
		sum += float64(r)
		for idx, dur := range histogramDurations {
			if r < dur {
				histogram[idx]++

				break
			}
		}
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
		minTime:   reads[0],
		maxTime:   reads[n-1],
		mean:      time.Duration(mean),
		p50:       percentile(50),
		p95:       percentile(95),
		p99:       percentile(99),
		stddev:    time.Duration(stdDev),
		histogram: histogram,
	}
}
