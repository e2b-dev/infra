package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNfsstatParse(t *testing.T) {
	t.Parallel()

	input := strings.TrimSpace(`
nfs v4 client        total:  7676067 
------------- ------------- --------
nfs v4 client         null:        2 
nfs v4 client      readdir:  1547278 
nfs v4 client  server_caps:        5 
nfs v4 client  exchange_id:        3 
nfs v4 client create_session:        1 
nfs v4 client     sequence:     1169 
nfs v4 client reclaim_comp:        1 
nfs v4 client test_stateid:        1 
nfs v4 client bind_conn_to_ses:      245 `)

	actual, err := nfsstatParse(input)
	require.NoError(t, err)

	assert.Equal(t, []nfsstat{
		{"nfs v4 client", "total", 7676067},
		{"nfs v4 client", "null", 2},
		{"nfs v4 client", "readdir", 1547278},
		{"nfs v4 client", "server_caps", 5},
		{"nfs v4 client", "exchange_id", 3},
		{"nfs v4 client", "create_session", 1},
		{"nfs v4 client", "sequence", 1169},
		{"nfs v4 client", "reclaim_comp", 1},
		{"nfs v4 client", "test_stateid", 1},
		{"nfs v4 client", "bind_conn_to_ses", 245},
	}, actual)
}

type mockExperiment struct {
	id string
}

func (m mockExperiment) setup(_ context.Context, _ *processor) error    { return nil }
func (m mockExperiment) teardown(_ context.Context, _ *processor) error { return nil }

func TestGenerateScenarios(t *testing.T) {
	t.Parallel()

	experiments := map[string]map[string]experiment{
		"a": {
			"1": mockExperiment{"a1"},
			"2": mockExperiment{"a2"},
		},
		"b": {
			"x": mockExperiment{"bx"},
			"y": mockExperiment{"by"},
		},
		"c": {
			"a": mockExperiment{"ca"},
			"b": mockExperiment{"cb"},
			"c": mockExperiment{"cc"},
		},
		"d": {
			"only": mockExperiment{"donly"},
		},
	}

	var scenarios []scenario
	for s := range generateScenarios(experiments) {
		scenarios = append(scenarios, s)
	}

	// Check if all combinations are present
	expected := []scenario{
		{elements: map[string]element{"a": {"1", mockExperiment{"a1"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"1", mockExperiment{"a1"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"1", mockExperiment{"a1"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"1", mockExperiment{"a1"}}, "b": {"y", mockExperiment{"by"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"1", mockExperiment{"a1"}}, "b": {"y", mockExperiment{"by"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"1", mockExperiment{"a1"}}, "b": {"y", mockExperiment{"by"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"2", mockExperiment{"a2"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"2", mockExperiment{"a2"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"2", mockExperiment{"a2"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"2", mockExperiment{"a2"}}, "b": {"y", mockExperiment{"by"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"2", mockExperiment{"a2"}}, "b": {"y", mockExperiment{"by"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}}},
		{elements: map[string]element{"a": {"2", mockExperiment{"a2"}}, "b": {"y", mockExperiment{"by"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}}},
	}

	assert.Len(t, scenarios, len(expected))
	assert.ElementsMatch(t, expected, scenarios)

	assert.Equal(t, "a=1; b=x; c=a; d=only", expected[0].Name())
}

func TestDumpResultsToCSV(t *testing.T) {
	t.Parallel()

	results := []result{
		{
			totalSuccessfulReads: 10,
			testDuration:         time.Second,
			scenario: scenario{
				elements: map[string]element{
					"exp1": {name: "val1"},
					"exp2": {name: "val2"},
				},
			},
			summary: durationSummary{
				minTime: 100 * time.Millisecond,
				p50:     200 * time.Millisecond,
				p95:     300 * time.Millisecond,
				p99:     400 * time.Millisecond,
				maxTime: 500 * time.Millisecond,
				stddev:  50 * time.Millisecond,
			},
		},
		{
			totalSuccessfulReads: 178,
			testDuration:         5 * time.Second,
			scenario: scenario{
				elements: map[string]element{
					"exp1": {name: "val3"},
					"exp2": {name: "val4"},
				},
			},
			summary: durationSummary{
				minTime: 110 * time.Millisecond,
				p50:     210 * time.Millisecond,
				p95:     310 * time.Millisecond,
				p99:     410 * time.Millisecond,
				maxTime: 510 * time.Millisecond,
				stddev:  51 * time.Millisecond,
			},
		},
	}

	tempDir := t.TempDir()
	tempCSV := filepath.Join(tempDir, "output.csv")
	defer os.Remove(tempCSV)

	err := dumpResultsToCSV(tempCSV, environmentMetadata{}, results)
	require.NoError(t, err)

	content, err := os.ReadFile(tempCSV)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 3)
	assert.Equal(t, "instance type,capacity (GB),read iops,max read bandwidth (MBps),exp1,exp2,files per second,min,mean,p50,p95,p99,max,stddev", lines[0])
	assert.Equal(t, ",0,0,0,val1,val2,10,100,0,200,300,400,500,50", lines[1])
	assert.Equal(t, ",0,0,0,val3,val4,35,110,0,210,310,410,510,51", lines[2])
}

func TestSummarizeDurations(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		summary := summarizeDurations(nil)
		assert.Equal(t, durationSummary{}, summary)
	})

	t.Run("single", func(t *testing.T) {
		t.Parallel()

		durations := []time.Duration{10 * time.Millisecond}
		summary := summarizeDurations(durations)
		assert.Equal(t, 10*time.Millisecond, summary.p50)
		assert.Equal(t, 10*time.Millisecond, summary.p95)
		assert.Equal(t, 10*time.Millisecond, summary.p99)
	})

	t.Run("multiple", func(t *testing.T) {
		t.Parallel()

		durations := make([]time.Duration, 100)
		for i := range 100 {
			durations[i] = time.Duration(i+1) * time.Millisecond
		}
		summary := summarizeDurations(durations)
		assert.Equal(t, 50*time.Millisecond, summary.p50)
		assert.Equal(t, 95*time.Millisecond, summary.p95)
		assert.Equal(t, 99*time.Millisecond, summary.p99)
	})

	t.Run("matches_user_report", func(t *testing.T) {
		t.Parallel()

		// 428 files, if p50 is 23ms, then throughput at concurrency 2 should be ~428 in 5s
		// throughput = 2 / 0.023 = 86.9 files/s
		// 5s * 86.9 = 434 files.
		// If we have 428 files in 5s, average latency per file = 5 / (428/2) = 0.02336s = 23.36ms.

		durations := make([]time.Duration, 428)
		for i := range durations {
			durations[i] = 23 * time.Millisecond
		}
		summary := summarizeDurations(durations)
		assert.Equal(t, 23*time.Millisecond, summary.p50)
	})
}

func TestRemoveAtIndex(t *testing.T) {
	t.Parallel()

	t.Run("middle", func(t *testing.T) {
		t.Parallel()

		items := []int{1, 2, 3, 4, 5}
		result := removeAtIndex(items, 2)
		assert.Equal(t, []int{1, 2, 4, 5}, result)
	})

	t.Run("first", func(t *testing.T) {
		t.Parallel()

		items := []int{1, 2, 3}
		result := removeAtIndex(items, 0)
		assert.Equal(t, []int{2, 3}, result)
	})

	t.Run("last", func(t *testing.T) {
		t.Parallel()

		items := []int{1, 2, 3}
		result := removeAtIndex(items, 2)
		assert.Equal(t, []int{1, 2}, result)
	})

	t.Run("original_slice_is_modified", func(t *testing.T) {
		t.Parallel()

		items := []int{1, 2, 3, 4, 5}
		_ = removeAtIndex(items, 2)
		// slices.Delete modifies the original slice and zeros out the tail
		assert.Equal(t, []int{1, 2, 4, 5, 0}, items)
	})

	t.Run("single_element", func(t *testing.T) {
		t.Parallel()

		items := []int{1}
		result := removeAtIndex(items, 0)
		assert.Equal(t, []int{}, result)
	})

	t.Run("empty_slice_panic", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			removeAtIndex([]int{}, 0)
		})
	})

	t.Run("out_of_bounds_panic", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			removeAtIndex([]int{1, 2}, 2)
		})
	})
}

func TestGetFilestoreMetadata(t *testing.T) {
	t.Parallel()

	metadata, err := getEnvironmentMetadata(t.Context(), "e2b-shared-disk-store", "us-west1-a")
	if err != nil {
		t.Skip("skipping test as it's not running in GCP")
	}

	assert.NotEqual(t, 0, metadata.FilestoreReadIOPS)
}
