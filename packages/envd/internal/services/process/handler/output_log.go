package handler

import (
	"bytes"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

const (
	// maxCommandOutputBytes caps how much command output (stdout + stderr combined)
	// we persist as logs per command execution. Output past this is dropped and a
	// single truncation marker is emitted. Protects the envd log exporter buffer and
	// downstream log storage from runaway commands. Tunable.
	maxCommandOutputBytes = 2 << 20 // 2 MiB

	// maxOutputLineBytes caps the length of a single emitted output line, keeping it
	// well under the exporter's per-line limit (192 KiB) even after JSON wrapping.
	maxOutputLineBytes = 64 << 10 // 64 KiB

	// perEventOverheadBytes approximates the fixed cost of one emitted log record
	// (timestamp, level, pid, stream, event_type, JSON framing) on top of the message
	// payload. Charging it against the budget also bounds the number of records a
	// command can produce: floods of tiny lines (e.g. `yes`) would otherwise emit
	// millions of records while consuming almost none of the byte budget.
	perEventOverheadBytes = 256

	truncationMarker = "[output truncated: command exceeded log capture limit]"
)

// commandLogBudget bounds the total output logged for one command. It is shared
// between the command's stdout and stderr outputLoggers so the cap is per-command,
// not per-stream. Safe for concurrent use by the two output goroutines.
type commandLogBudget struct {
	remaining atomic.Int64
	truncated sync.Once
}

func newCommandLogBudget() *commandLogBudget {
	b := &commandLogBudget{}
	b.remaining.Store(maxCommandOutputBytes)

	return b
}

// markTruncated emits the truncation marker exactly once across both streams.
func (b *commandLogBudget) markTruncated(logger *zerolog.Logger) {
	b.truncated.Do(func() {
		logger.Warn().
			Str("event_type", "process_output").
			Msg(truncationMarker)
	})
}

// outputLogger turns the arbitrary chunk stream from a process pipe into clean,
// per-line log entries. It is used by a single output goroutine (one per stream),
// so its line buffer needs no synchronization; only the shared budget is concurrent.
type outputLogger struct {
	logger *zerolog.Logger
	budget *commandLogBudget
	stream string        // "stdout" or "stderr"
	pid    func() uint32 // process pid, resolved lazily (only valid after the process starts)
	buf    []byte        // accumulated bytes of the current, not-yet-terminated line
}

func newOutputLogger(logger *zerolog.Logger, budget *commandLogBudget, stream string, pid func() uint32) *outputLogger {
	return &outputLogger{
		logger: logger,
		budget: budget,
		stream: stream,
		pid:    pid,
	}
}

// write appends a chunk read from the pipe and emits a log line for every complete
// (newline-terminated) line. An over-long line with no newline is flushed in capped
// segments so the buffer can't grow unbounded.
func (o *outputLogger) write(chunk []byte) {
	o.buf = append(o.buf, chunk...)

	for {
		if idx := bytes.IndexByte(o.buf, '\n'); idx >= 0 {
			line := bytes.TrimSuffix(o.buf[:idx], []byte("\r"))
			o.emitLine(line)
			o.buf = o.buf[idx+1:]

			continue
		}

		if len(o.buf) >= maxOutputLineBytes {
			o.emitLine(o.buf[:maxOutputLineBytes])
			o.buf = o.buf[maxOutputLineBytes:]

			continue
		}

		break
	}
}

// flush emits any trailing line that was not newline-terminated. Call once at EOF.
func (o *outputLogger) flush() {
	if len(o.buf) > 0 {
		o.emitLine(o.buf)
		o.buf = nil
	}
}

func (o *outputLogger) emitLine(line []byte) {
	if len(line) == 0 {
		return
	}

	if o.budget.remaining.Load() <= 0 {
		o.budget.markTruncated(o.logger)

		return
	}

	if len(line) > maxOutputLineBytes {
		line = line[:maxOutputLineBytes]
	}

	// pid + event_type scope retrieval to a single command's output; the line
	// content goes into "message" so the sandbox-logs search filter works too.
	o.logger.Info().
		Str("event_type", "process_output").
		Str("stream", o.stream).
		Uint32("pid", o.pid()).
		Msg(string(line))

	if o.budget.remaining.Add(-(int64(len(line)) + perEventOverheadBytes)) <= 0 {
		o.budget.markTruncated(o.logger)
	}
}
