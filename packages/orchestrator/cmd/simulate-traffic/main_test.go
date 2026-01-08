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
		{"a": {"1", mockExperiment{"a1"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"1", mockExperiment{"a1"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"1", mockExperiment{"a1"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"1", mockExperiment{"a1"}}, "b": {"y", mockExperiment{"by"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"1", mockExperiment{"a1"}}, "b": {"y", mockExperiment{"by"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"1", mockExperiment{"a1"}}, "b": {"y", mockExperiment{"by"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"2", mockExperiment{"a2"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"2", mockExperiment{"a2"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"2", mockExperiment{"a2"}}, "b": {"x", mockExperiment{"bx"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"2", mockExperiment{"a2"}}, "b": {"y", mockExperiment{"by"}}, "c": {"a", mockExperiment{"ca"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"2", mockExperiment{"a2"}}, "b": {"y", mockExperiment{"by"}}, "c": {"b", mockExperiment{"cb"}}, "d": {"only", mockExperiment{"donly"}}},
		{"a": {"2", mockExperiment{"a2"}}, "b": {"y", mockExperiment{"by"}}, "c": {"c", mockExperiment{"cc"}}, "d": {"only", mockExperiment{"donly"}}},
	}

	assert.Len(t, scenarios, len(expected))
	assert.ElementsMatch(t, expected, scenarios)

	assert.Equal(t, "a=1; b=x; c=a; d=only", expected[0].Name())
}

func TestDumpResultsToCSV(t *testing.T) {
	t.Parallel()

	results := []result{
		{
			scenario: scenario{
				"exp1": {name: "val1"},
				"exp2": {name: "val2"},
			},
			summary: durationSummary{
				count:   10,
				minTime: 100 * time.Millisecond,
				p50:     200 * time.Millisecond,
				p95:     300 * time.Millisecond,
				p99:     400 * time.Millisecond,
				maxTime: 500 * time.Millisecond,
				stddev:  50 * time.Millisecond,
			},
		},
		{
			scenario: scenario{
				"exp1": {name: "val3"},
				"exp2": {name: "val4"},
			},
			summary: durationSummary{
				count:   20,
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

	err := dumpResultsToCSV(tempCSV, results)
	require.NoError(t, err)

	content, err := os.ReadFile(tempCSV)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 3)
	assert.Equal(t, "exp1,exp2,count,min,p50,p95,p99,max,stddev", lines[0])
	assert.Equal(t, "val1,val2,10,100,200,300,400,500,50", lines[1])
	assert.Equal(t, "val3,val4,20,110,210,310,410,510,51", lines[2])
}
