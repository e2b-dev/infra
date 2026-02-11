package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

func dumpResultsToCSV(experiments map[string]map[string]experiment, path string, metadata environmentMetadata, results []result, allExperiments bool) error {
	// 1. Identify all experiment keys
	experimentKeys := make([]string, 0, len(experiments))
	for k, opts := range experiments {
		if !allExperiments && len(opts) == 1 {
			continue
		}

		experimentKeys = append(experimentKeys, k)
	}
	slices.Sort(experimentKeys)

	// 2. Identify all metrics
	metrics := []string{"min", "mean", "p50", "p95", "p99", "max", "stddev"}
	for _, h := range histogramDurations {
		metrics = append(metrics, fmt.Sprintf("<%s", h))
	}

	// 3. Open output.csv
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer safeClose(f)

	// 4. Write header
	header := []string{"instance type"}
	header = append(header, experimentKeys...)
	header = append(header, metrics...)
	if _, err := fmt.Fprintln(f, strings.Join(header, ",")); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// 5. Write data rows
	for _, res := range results {
		row := make([]string, 0, len(header))

		// metadata values
		row = append(row,
			metadata.ClientMachineType,
		)

		// Experiment values
		for _, k := range experimentKeys {
			val := ""
			if e, ok := res.scenario.elements[k]; ok {
				val = e.name
			}
			row = append(row, val)
		}

		// Metric values
		s := res.summary
		row = append(row,
			toMillis(s.minTime),
			toMillis(s.mean),
			toMillis(s.p50),
			toMillis(s.p95),
			toMillis(s.p99),
			toMillis(s.maxTime),
			toMillis(s.stddev),
		)

		for _, count := range s.histogram {
			portion := float64(count) / float64(res.totalSuccessfulReads)
			row = append(row, fmt.Sprintf("%.0f%%", portion*100))
		}

		if _, err := fmt.Fprintln(f, strings.Join(row, ",")); err != nil {
			return fmt.Errorf("failed to write row: %w", err)
		}
	}

	fmt.Println("\nResults dumped to " + path)

	return nil
}

func toMillis(minTime time.Duration) string {
	return strconv.FormatInt(minTime.Milliseconds(), 10)
}
