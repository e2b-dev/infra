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

	// maxCommandOutputRecords caps how many log records one command may emit. The
	// byte budget alone does not bound record count: a flood of tiny lines (e.g.
	// `yes`) stays within the byte cap while emitting millions of records, each
	// carrying ~200 bytes of field/JSON framing through the exporter. Tunable.
	maxCommandOutputRecords = 10_000

	// maxOutputLineBytes caps the length of a single emitted output line, keeping it
	// well under the exporter's per-line limit (192 KiB) even after JSON wrapping.
	maxOutputLineBytes = 64 << 10 // 64 KiB

	truncationMarker = "[output truncated: command exceeded log capture limit]"
)

// commandLogBudget bounds the total output logged for one command, by bytes of
// payload and by number of records. It is shared between the command's stdout and
// stderr outputLoggers so the caps are per-command, not per-stream. Safe for
// concurrent use by the two output goroutines.
type commandLogBudget struct {
	remainingBytes   atomic.Int64
	remainingRecords atomic.Int64
	truncated        sync.Once
}

func newCommandLogBudget() *commandLogBudget {
	b := &commandLogBudget{}
	b.remainingBytes.Store(maxCommandOutputBytes)
	b.remainingRecords.Store(maxCommandOutputRecords)

	return b
}

// markTruncated emits the truncation marker exactly once across both streams. The
// marker carries the same pid/stream fields as regular output lines so it survives
// the pid-scoped retrieval filter and command-specific queries report truncation.
func (b *commandLogBudget) markTruncated(logger *zerolog.Logger, stream string, pid uint32) {
	b.truncated.Do(func() {
		logger.Warn().
			Str("event_type", "process_output").
			Str("stream", stream).
			Uint32("pid", pid).
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

// emitLine persists one completed output line as a log record. Blank lines are
// emitted too (with an empty message): they are part of the command's real output,
// and dropping them would make pid-filtered retrieval diverge from the live stream.
func (o *outputLogger) emitLine(line []byte) {
	if o.budget.remainingBytes.Load() <= 0 || o.budget.remainingRecords.Load() <= 0 {
		o.budget.markTruncated(o.logger, o.stream, o.pid())

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

	bytesLeft := o.budget.remainingBytes.Add(-int64(len(line)))
	recordsLeft := o.budget.remainingRecords.Add(-1)
	if bytesLeft <= 0 || recordsLeft <= 0 {
		o.budget.markTruncated(o.logger, o.stream, o.pid())
	}
}
