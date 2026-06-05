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
	Cid       string `json:"cid"`
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

func newCapturingLogger(cid string) (*zerolog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := zerolog.New(buf).With().Str("cid", cid).Logger()

	return &l, buf
}

// TestOutputLogger_SplitsLines verifies output is emitted one log line per newline,
// carrying the cid (bound on the logger), the stream, and the line content as message.
func TestOutputLogger_SplitsLines(t *testing.T) {
	t.Parallel()

	logger, buf := newCapturingLogger("cmd-1")
	o := newOutputLogger(logger, newCommandLogBudget(), "stdout")

	// Chunk boundaries deliberately split a line ("hel" + "lo\nworld").
	o.write([]byte("hel"))
	o.write([]byte("lo\nwor"))
	o.write([]byte("ld\n"))
	// Trailing partial line with no newline is only emitted on flush.
	o.write([]byte("tail"))

	lines := parseLines(t, buf.Bytes())
	require.Len(t, lines, 2)
	assert.Equal(t, logLine{EventType: "process_output", Stream: "stdout", Cid: "cmd-1", Message: "hello", Level: "info"}, lines[0])
	assert.Equal(t, "world", lines[1].Message)

	o.flush()
	lines = parseLines(t, buf.Bytes())
	require.Len(t, lines, 3)
	assert.Equal(t, "tail", lines[2].Message)
}

// TestOutputLogger_TrimsCarriageReturn verifies CRLF line endings are normalized.
func TestOutputLogger_TrimsCarriageReturn(t *testing.T) {
	t.Parallel()

	logger, buf := newCapturingLogger("cmd-1")
	o := newOutputLogger(logger, newCommandLogBudget(), "stderr")

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

	logger, buf := newCapturingLogger("cmd-1")
	budget := newCommandLogBudget()
	stdoutLog := newOutputLogger(logger, budget, "stdout")
	stderrLog := newOutputLogger(logger, budget, "stderr")

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
		} else {
			emitted++
		}
	}

	assert.Equal(t, 1, markers, "expected exactly one truncation marker")
	// We should have emitted roughly the cap worth of lines, not all of them.
	assert.Less(t, emitted, totalLines, "output past the cap should be dropped")
	assert.Positive(t, emitted, "output up to the cap should be emitted")
}
