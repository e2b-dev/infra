package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var (
	_ io.Reader   = (*offsetReader)(nil)
	_ RangeReader = (*sectionReader)(nil)
	_ RangeReader = (*observableReader)(nil)
	_ RangeReader = (*rangeReader)(nil)
	_ RangeReader = (*captureReader)(nil)
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

// rangeReader adapts an io.ReadCloser into a RangeReader by ignoring the
// Close context.
type rangeReader struct {
	io.ReadCloser
}

func NewRangeReader(rc io.ReadCloser) RangeReader { return &rangeReader{ReadCloser: rc} }

func (p *rangeReader) Close(context.Context) error {
	return p.ReadCloser.Close()
}

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

func (r *sectionReader) Close(context.Context) error {
	return r.file.Close()
}

// captureReader tees every read byte into a buffer and hands the captured
// bytes to onClose on Close. Used by the cache writeback paths.
//
// drainOnClose=true reads inner to EOF on Close even if the caller above hasn't
// consumed everything. Needed when capturing under a decoder that stops short
// of EOF on its source (e.g. lz4.Reader skips the 4-byte EndMark).
type captureReader struct {
	inner        RangeReader
	buf          *bytes.Buffer
	onClose      func(ctx context.Context, captured []byte)
	drainOnClose bool
}

func newCaptureReader(inner RangeReader, capHint int, drainOnClose bool, onClose func(context.Context, []byte)) *captureReader {
	return &captureReader{
		inner:        inner,
		buf:          bytes.NewBuffer(make([]byte, 0, capHint)),
		onClose:      onClose,
		drainOnClose: drainOnClose,
	}
}

func (r *captureReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.buf.Write(p[:n])
	}

	return n, err
}

func (r *captureReader) Close(ctx context.Context) error {
	if r.drainOnClose {
		_, _ = io.Copy(io.Discard, r)
	}
	err := r.inner.Close(ctx)
	r.onClose(ctx, r.buf.Bytes())

	return err
}

// observableReader layers OTEL observability (legacy per-backend timer + span)
// onto an inner RangeReader, all applied on Close. timer and span are optional;
// pass nil if unused.
type observableReader struct {
	inner RangeReader
	timer *telemetry.Stopwatch
	span  trace.Span

	bytes   int64
	readErr error
}

func newObservableReader(inner RangeReader, timer *telemetry.Stopwatch, span trace.Span) *observableReader {
	return &observableReader{inner: inner, timer: timer, span: span}
}

func (r *observableReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.bytes += int64(n)

	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}

	return n, err
}

func (r *observableReader) Close(ctx context.Context) error {
	closeErr := r.inner.Close(ctx)

	if r.timer != nil {
		if r.readErr != nil || closeErr != nil {
			r.timer.Failure(ctx, r.bytes)
		} else {
			r.timer.Success(ctx, r.bytes)
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
