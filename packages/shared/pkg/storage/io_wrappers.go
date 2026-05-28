package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var (
	_ io.Reader   = (*offsetReader)(nil)
	_ io.Reader   = (*meteredReader)(nil)
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
// Close context. It does not meter, so Close returns nil stats.
type rangeReader struct {
	io.ReadCloser
}

func NewRangeReader(rc io.ReadCloser) RangeReader { return &rangeReader{ReadCloser: rc} }

func (p *rangeReader) Close(context.Context) (*ReadStats, error) {
	return nil, p.ReadCloser.Close()
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

func (r *sectionReader) Close(context.Context) (*ReadStats, error) {
	return nil, r.file.Close()
}

// meteredReader records cumulative time and bytes spent pulling from inner so
// a decoder built on top can separate source-read wall from decompression CPU.
// Single-goroutine: Read is sequential, stats are read after EOF in Close.
type meteredReader struct {
	inner io.Reader
	nanos int64
	bytes int64
}

func (m *meteredReader) Read(p []byte) (int, error) {
	t0 := time.Now()
	n, err := m.inner.Read(p)
	m.nanos += int64(time.Since(t0))
	m.bytes += int64(n)

	return n, err
}

// captureReader tees every read byte into a buffer and hands the captured
// bytes to onClose on Close. Used by the compressed cache writeback path.
type captureReader struct {
	inner   RangeReader
	buf     *bytes.Buffer
	onClose func(ctx context.Context, captured []byte)
}

func newCaptureReader(inner RangeReader, capHint int, onClose func(context.Context, []byte)) *captureReader {
	return &captureReader{
		inner:   inner,
		buf:     bytes.NewBuffer(make([]byte, 0, capHint)),
		onClose: onClose,
	}
}

func (r *captureReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.buf.Write(p[:n])
	}

	return n, err
}

func (r *captureReader) Close(ctx context.Context) (*ReadStats, error) {
	stats, err := r.inner.Close(ctx)
	r.onClose(ctx, r.buf.Bytes())

	return stats, err
}

// observableReader layers OTEL observability onto an inner RangeReader, all
// applied on Close. The with* builder methods are optional and chainable.
type observableReader struct {
	inner RangeReader

	timer *telemetry.Stopwatch
	span  trace.Span

	bytes   int64
	readErr error

	read time.Duration
}

func newObservableReader(inner RangeReader) *observableReader {
	return &observableReader{inner: inner}
}

func (r *observableReader) withTimer(t *telemetry.Stopwatch) *observableReader {
	r.timer = t

	return r
}

func (r *observableReader) withSpan(s trace.Span) *observableReader {
	r.span = s

	return r
}

func (r *observableReader) Read(p []byte) (int, error) {
	t0 := time.Now()
	n, err := r.inner.Read(p)
	r.read += time.Since(t0)
	r.bytes += int64(n)

	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}

	return n, err
}

func (r *observableReader) Close(ctx context.Context) (*ReadStats, error) {
	stats, closeErr := r.inner.Close(ctx)

	if stats == nil {
		stats = &ReadStats{
			CompressedBytes:   r.bytes,
			UncompressedBytes: r.bytes,
			Read:              r.read,
		}
	}

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

	return stats, closeErr
}
