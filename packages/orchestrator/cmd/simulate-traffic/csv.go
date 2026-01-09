package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

func dumpResultsToCSV(path string, metadata environmentMetadata, results []result) error {
	// 1. Identify all experiment keys
	experimentKeysMap := make(map[string]struct{})
	for _, res := range results {
		for k := range res.scenario.elements {
			experimentKeysMap[k] = struct{}{}
		}
	}
	experimentKeys := make([]string, 0, len(experimentKeysMap))
	for k := range experimentKeysMap {
		experimentKeys = append(experimentKeys, k)
	}
	slices.Sort(experimentKeys)

	// 2. Identify all metrics
	metrics := []string{"files per second", "min", "mean", "p50", "p95", "p99", "max", "stddev"}

	// 3. Open output.csv
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer safeClose(f)

	// 4. Write header
	header := []string{"instance type", "capacity (GB)", "read iops", "max read bandwidth (MBps)"}
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
			toIntString(metadata.FilestoreCapacityGB),
			toIntString(metadata.FilestoreReadIOPS),
			toIntString(metadata.FilestoreMaxReadMBps),
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
			toIntString(int(float64(res.totalSuccessfulReads)/res.testDuration.Seconds())),
			toMillis(s.minTime),
			toMillis(s.mean),
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

	fmt.Println("\nResults dumped to " + path)

	return nil
}

func toMillis(minTime time.Duration) string {
	return strconv.FormatInt(minTime.Milliseconds(), 10)
}

func toIntString(i int) string {
	return strconv.Itoa(i)
}
