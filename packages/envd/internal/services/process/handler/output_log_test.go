package handler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type logLine struct {
	EventType string `json:"event_type"`
	Stream    string `json:"stream"`
	Pid       uint32 `json:"pid"`
	Message   string `json:"message"`
	Level     string `json:"level"`
}

func parseLines(t *testing.T, raw []byte) []logLine {
	t.Helper()

	var lines []logLine
	for b := range bytes.SplitSeq(bytes.TrimSpace(raw), []byte("\n")) {
		if len(b) == 0 {
			continue
		}

		var l logLine
		require.NoError(t, json.Unmarshal(b, &l))
		lines = append(lines, l)
	}

	return lines
}

func newCapturingLogger() (*zerolog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := zerolog.New(buf)

	return &l, buf
}

const testPid = uint32(1234)

func testPidFn() uint32 { return testPid }

// TestOutputLogger_SplitsLines verifies output is emitted one log line per newline,
// carrying the pid, the stream, and the line content as message.
func TestOutputLogger_SplitsLines(t *testing.T) {
	t.Parallel()

	logger, buf := newCapturingLogger()
	o := newOutputLogger(logger, newCommandLogBudget(), "stdout", testPidFn)

	// Chunk boundaries deliberately split a line ("hel" + "lo\nworld").
	o.write([]byte("hel"))
	o.write([]byte("lo\nwor"))
	o.write([]byte("ld\n"))
	// Trailing partial line with no newline is only emitted on flush.
	o.write([]byte("tail"))

	lines := parseLines(t, buf.Bytes())
	require.Len(t, lines, 2)
	assert.Equal(t, logLine{EventType: "process_output", Stream: "stdout", Pid: testPid, Message: "hello", Level: "info"}, lines[0])
	assert.Equal(t, "world", lines[1].Message)

	o.flush()
	lines = parseLines(t, buf.Bytes())
	require.Len(t, lines, 3)
	assert.Equal(t, "tail", lines[2].Message)
}

// TestOutputLogger_TrimsCarriageReturn verifies CRLF line endings are normalized.
func TestOutputLogger_TrimsCarriageReturn(t *testing.T) {
	t.Parallel()

	logger, buf := newCapturingLogger()
	o := newOutputLogger(logger, newCommandLogBudget(), "stderr", testPidFn)

	o.write([]byte("crlf-line\r\n"))

	lines := parseLines(t, buf.Bytes())
	require.Len(t, lines, 1)
	assert.Equal(t, "crlf-line", lines[0].Message)
	assert.Equal(t, "stderr", lines[0].Stream)
}

// TestOutputLogger_PerCommandCap verifies the shared budget caps total output across
// both streams and emits exactly one truncation marker.
func TestOutputLogger_PerCommandCap(t *testing.T) {
	t.Parallel()

	logger, buf := newCapturingLogger()
	budget := newCommandLogBudget()
	stdoutLog := newOutputLogger(logger, budget, "stdout", testPidFn)
	stderrLog := newOutputLogger(logger, budget, "stderr", testPidFn)

	// One full line is ~maxOutputLineBytes; emit well past maxCommandOutputBytes.
	line := strings.Repeat("a", maxOutputLineBytes) + "\n"
	totalLines := (maxCommandOutputBytes / maxOutputLineBytes) + 10
	for range totalLines {
		stdoutLog.write([]byte(line))
	}
	// The other stream should also observe the exhausted budget.
	stderrLog.write([]byte("more\n"))

	lines := parseLines(t, buf.Bytes())

	var markers, emitted int
	for _, l := range lines {
		if l.Message == truncationMarker {
			markers++
			// The marker must carry the same fields as regular output lines so it
			// survives the pid-scoped retrieval filter.
			assert.Equal(t, "process_output", l.EventType)
			assert.Equal(t, testPid, l.Pid)
			assert.NotEmpty(t, l.Stream)
		} else {
			emitted++
		}
	}

	assert.Equal(t, 1, markers, "expected exactly one truncation marker")
	// We should have emitted roughly the cap worth of lines, not all of them.
	assert.Less(t, emitted, totalLines, "output past the cap should be dropped")
	assert.Positive(t, emitted, "output up to the cap should be emitted")
}

// TestOutputLogger_TinyLineFloodBoundsRecordCount verifies the record cap bounds
// the number of emitted records, not just payload bytes: a command emitting tiny
// lines (e.g. `yes`) must not turn the byte budget into millions of log records.
func TestOutputLogger_TinyLineFloodBoundsRecordCount(t *testing.T) {
	t.Parallel()

	logger, buf := newCapturingLogger()
	o := newOutputLogger(logger, newCommandLogBudget(), "stdout", testPidFn)

	// Each line carries a 1-byte payload, so the record cap trips long before the
	// byte budget. Write comfortably past it.
	const chunkLines = 1024
	chunk := []byte(strings.Repeat("y\n", chunkLines))
	for written := 0; written < maxCommandOutputRecords+2*chunkLines; written += chunkLines {
		o.write(chunk)
	}

	lines := parseLines(t, buf.Bytes())

	var markers, emitted int
	for _, l := range lines {
		if l.Message == truncationMarker {
			markers++
		} else {
			emitted++
		}
	}

	assert.Equal(t, 1, markers, "expected exactly one truncation marker")
	assert.Equal(t, maxCommandOutputRecords, emitted, "record count must be bounded by the per-command record cap")
}
