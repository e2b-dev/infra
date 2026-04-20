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
	_ io.Reader     = (*offsetReader)(nil)  // ReaderAt → sequential Reader adapter
	_ io.ReadCloser = (*timedReader)(nil)   // OTEL timer + span instrumentation
	_ io.ReadCloser = (*cancelReader)(nil)  // context cancel on Close
	_ io.ReadCloser = (*sectionReader)(nil) // os.File SectionReader for range reads
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

// timedReader wraps an io.ReadCloser with OTEL instrumentation.
// Counts bytes read, tracks read errors, and on Close records the timer
// as Success or Failure and optionally ends the span.
// span may be nil (e.g. GCS path which manages its own spans).
func newTimedReader(inner io.ReadCloser, timer *telemetry.Stopwatch, span trace.Span, ctx context.Context) *timedReader {
	return &timedReader{inner: inner, timer: timer, span: span, ctx: ctx}
}

type timedReader struct {
	inner   io.ReadCloser
	timer   *telemetry.Stopwatch
	span    trace.Span
	ctx     context.Context //nolint:containedctx // needed to record timer/span on Close
	bytes   int64
	readErr error
}

func (r *timedReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.bytes += int64(n)

	if err != nil && err != io.EOF {
		r.readErr = err
	}

	return n, err
}

func (r *timedReader) Close() error {
	closeErr := r.inner.Close()

	if r.readErr != nil || closeErr != nil {
		r.timer.Failure(r.ctx, r.bytes)
	} else {
		r.timer.Success(r.ctx, r.bytes)
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

// sectionReader wraps an os.File with a SectionReader for range reads.
type sectionReader struct {
	io.Reader

	file *os.File
}

func (r *sectionReader) Close() error {
	return r.file.Close()
}
