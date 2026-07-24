package sandboxlogs

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

func TestOrderSQL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ASC", orderSQL(SortOrderForward))
	assert.Equal(t, "DESC", orderSQL(SortOrderBackward))
	// Unknown values default to ascending.
	assert.Equal(t, "ASC", orderSQL(SortOrder(42)))
}

func TestTimestampOrderSQL(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		"toStartOfFiveMinutes(Timestamp) ASC, Timestamp ASC",
		timestampOrderSQL(SortOrderForward),
	)
	assert.Equal(
		t,
		"toStartOfFiveMinutes(Timestamp) DESC, Timestamp DESC",
		timestampOrderSQL(SortOrderBackward),
	)
}

func TestAtLeastLevels(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"error"}, atLeastLevels(logs.LevelError))
	assert.Equal(t, []string{"warn", "error"}, atLeastLevels(logs.LevelWarn))
	assert.Equal(t, []string{"", "info", "warn", "error"}, atLeastLevels(logs.LevelInfo))
	assert.Equal(t, []string{"", "debug", "info", "warn", "error"}, atLeastLevels(logs.LevelDebug))
}

func TestUnixNano(t *testing.T) {
	t.Parallel()

	ts := time.Date(2024, 1, 2, 3, 4, 5, 6, time.UTC)
	assert.Equal(t, ts.UnixNano(), unixNano(ts))

	// Non-UTC input must be normalized to the same instant.
	loc := time.FixedZone("test", 3600)
	local := ts.In(loc)
	assert.Equal(t, ts.UnixNano(), unixNano(local))
}

func TestTimestampRangeFilters(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{
		"toStartOfFiveMinutes(Timestamp) >= toStartOfFiveMinutes(fromUnixTimestamp64Nano({start:Int64}))",
		"toStartOfFiveMinutes(Timestamp) <= toStartOfFiveMinutes(fromUnixTimestamp64Nano({end:Int64}))",
		"Timestamp >= fromUnixTimestamp64Nano({start:Int64})",
		"Timestamp <= fromUnixTimestamp64Nano({end:Int64})",
	}, timestampRangeFilters())
}

func TestSandboxLogsSelectUsesCompatibleAttributeNames(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{
		teamIDAttribute,
		sandboxIDAttribute,
		templateIDAttribute,
		buildIDAttribute,
	} {
		assert.Contains(t, sandboxLogsSelect, expression)
	}
	assert.Contains(t, sandboxLogsSelect, "LogAttributes['legacy.raw']")
	assert.NotContains(t, sandboxLogsSelect, "    Body,\n    Body,")

	assert.Equal(t, "coalesce(nullIf(LogAttributes['team_id'], ''), LogAttributes['team.id'])", teamIDAttribute)
	assert.Equal(t, "coalesce(nullIf(LogAttributes['sandbox_id'], ''), LogAttributes['sandbox.id'])", sandboxIDAttribute)
	assert.Equal(t, "coalesce(nullIf(LogAttributes['template_id'], ''), LogAttributes['template.id'])", templateIDAttribute)
	assert.Equal(t, "coalesce(nullIf(LogAttributes['build_id'], ''), LogAttributes['build.id'])", buildIDAttribute)
}

func TestRowToLogEntry(t *testing.T) {
	t.Parallel()

	ts := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	r := row{
		Timestamp: ts,
		Level:     "warn",
		Message:   "hello",
		Raw:       "hello",
		Fields:    `{"key":"value","n":"2"}`,
	}

	var parseErr error
	entry := r.toLogEntry(func(err error) { parseErr = err })

	require.NoError(t, parseErr)
	assert.Equal(t, ts, entry.Timestamp)
	assert.Equal(t, "hello", entry.Raw)
	assert.Equal(t, logs.LevelWarn, entry.Level)
	assert.Equal(t, "hello", entry.Message)
	assert.Equal(t, map[string]string{"key": "value", "n": "2"}, entry.Fields)
}

func TestRowToLogEntryPreservesMigratedRawAndMergesLegacyFields(t *testing.T) {
	t.Parallel()

	const raw = `{"timestamp":"2024-05-06T07:08:09Z","message":"original","level":"error","old":"field"}`
	r := row{
		Timestamp: time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC),
		Level:     "warn",
		Message:   "structured",
		Raw:       raw,
		Fields: `{
			"sandbox_id":"sandbox-new",
			"legacy.raw":"ignored-attribute-copy",
			"legacy.fields":"{\"old\":\"field\",\"sandbox_id\":\"sandbox-old\"}"
		}`,
	}

	entry := r.toLogEntry(func(err error) { t.Fatal(err) })

	if entry.Raw != raw {
		t.Errorf("Raw = %q, want exact legacy raw %q", entry.Raw, raw)
	}
	assert.Equal(t, map[string]string{
		"old":        "field",
		"sandbox_id": "sandbox-new",
	}, entry.Fields)
}

func TestRowToLogEntryReconstructsRawJSON(t *testing.T) {
	t.Parallel()

	ts := time.Date(2024, 5, 6, 7, 8, 9, 123456789, time.FixedZone("test", 3600))
	r := row{
		Timestamp: ts,
		Level:     "WARN",
		Message:   "structured message",
		Fields:    `{"team.id":"team-a","legacy.raw":"","legacy.fields":"{\"old\":\"field\"}"}`,
	}

	entry := r.toLogEntry(func(err error) { t.Fatal(err) })

	assert.Equal(t, logs.LevelWarn, entry.Level)
	assert.Equal(t, "structured message", entry.Message)
	assert.Equal(t, map[string]string{"old": "field", "team.id": "team-a"}, entry.Fields)
	assert.True(t, json.Valid([]byte(entry.Raw)))

	line := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(entry.Raw), &line))
	assert.Equal(t, ts.UTC().Format(time.RFC3339Nano), line["timestamp"])
	assert.Equal(t, "structured message", line["message"])
	assert.Equal(t, "warn", line["level"])
	assert.Equal(t, "team-a", line["team.id"])
	assert.Equal(t, "field", line["old"])
	assert.NotContains(t, entry.Raw, "legacy.raw")
	assert.NotContains(t, entry.Raw, "legacy.fields")
}

func TestRowToLogEntryEmptyFields(t *testing.T) {
	t.Parallel()

	r := row{Message: "m", Fields: ""}

	called := false
	entry := r.toLogEntry(func(error) { called = true })

	assert.False(t, called, "empty fields must not trigger a parse error")
	assert.Equal(t, map[string]string{}, entry.Fields)
}

func TestRowToLogEntryInvalidFieldsPreservesRaw(t *testing.T) {
	t.Parallel()

	r := row{
		Raw:    "original-raw",
		Fields: `{"broken": `,
	}

	var gotErr error
	entry := r.toLogEntry(func(err error) { gotErr = err })

	require.Error(t, gotErr)
	// Malformed fields fall back to an empty map but raw is preserved.
	assert.Equal(t, map[string]string{}, entry.Fields)
	assert.Equal(t, "original-raw", entry.Raw)
}

func TestRowToLogEntryNilCallbackDoesNotPanic(t *testing.T) {
	t.Parallel()

	r := row{Fields: `{"broken": `}

	assert.NotPanics(t, func() {
		entry := r.toLogEntry(nil)
		assert.Equal(t, map[string]string{}, entry.Fields)
	})
}
