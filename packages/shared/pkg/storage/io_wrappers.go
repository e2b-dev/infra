package storage

import (
	"context"
	"io"
	"os"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// io.ReadCloser / io.Reader wrappers used in the storage read path.
var (
	_ io.Reader     = (*offsetReader)(nil)       // ReaderAt → sequential Reader adapter
	_ io.ReadCloser = (*instrumentedReader)(nil) // OTEL timer or span on Close
	_ io.ReadCloser = (*cancelReader)(nil)       // context cancel on Close
	_ io.ReadCloser = (*sectionReader)(nil)      // os.File SectionReader for range reads
)

// offsetReader adapts an io.ReaderAt into a sequential io.Reader
// starting at the given offset.
type offsetReader struct {
	wrapped io.ReaderAt
	offset  int64
}

func (r *offsetReader) Read(p []byte) (n int, err error) {
	n, err = r.wrapped.ReadAt(p, r.offset)
	r.offset += int64(n)

	return
}

func newOffsetReader(reader io.ReaderAt, offset int64) *offsetReader {
	return &offsetReader{reader, offset}
}

// instrumentedReader wraps an io.ReadCloser with OTEL instrumentation.
// Counts bytes read, tracks read errors, and on Close records the timer
// as Success/Failure and/or ends the span. Construct via newTimedReader
// or newSpanReader; exactly one of timer/span is set per call site.
type instrumentedReader struct {
	inner   io.ReadCloser
	timer   *telemetry.Stopwatch
	span    trace.Span
	ctx     context.Context //nolint:containedctx // needed to record timer/span on Close
	bytes   int64
	readErr error
}

func newTimedReader(inner io.ReadCloser, timer *telemetry.Stopwatch, ctx context.Context) *instrumentedReader {
	return &instrumentedReader{inner: inner, timer: timer, ctx: ctx}
}

func newSpanReader(inner io.ReadCloser, span trace.Span, ctx context.Context) *instrumentedReader {
	return &instrumentedReader{inner: inner, span: span, ctx: ctx}
}

func (r *instrumentedReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.bytes += int64(n)

	if err != nil && err != io.EOF {
		r.readErr = err
	}

	return n, err
}

func (r *instrumentedReader) Close() error {
	closeErr := r.inner.Close()

	if r.timer != nil {
		if r.readErr != nil || closeErr != nil {
			r.timer.Failure(r.ctx, r.bytes)
		} else {
			r.timer.Success(r.ctx, r.bytes)
		}
	}

	if r.span != nil {
		if closeErr != nil {
			recordError(r.span, closeErr)
		} else if r.readErr != nil {
			recordError(r.span, r.readErr)
		}

		r.span.End()
	}

	return closeErr
}

// cancelReader calls a CancelFunc on Close, ensuring the context used
// to create the reader is cleaned up.
type cancelReader struct {
	io.ReadCloser

	cancel context.CancelFunc
}

func (r *cancelReader) Close() error {
	defer r.cancel()

	return r.ReadCloser.Close()
}

// sectionReader exposes a bounded section of an os.File as a ReadCloser,
// closing the underlying file on Close.
type sectionReader struct {
	*io.SectionReader

	file *os.File
}

func newSectionReader(f *os.File, off, length int64) *sectionReader {
	return &sectionReader{
		SectionReader: io.NewSectionReader(f, off, length),
		file:          f,
	}
}

func (r *sectionReader) Close() error {
	return r.file.Close()
}
