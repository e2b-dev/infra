package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"go.opentelemetry.io/otel/trace"
)

var (
	_ io.Reader   = (*meteredReader)(nil)
	_ RangeReader = (*sectionReader)(nil)
	_ RangeReader = (*spanReader)(nil)
	_ RangeReader = (*rangeReader)(nil)
	_ RangeReader = (*captureReader)(nil)
)

// readMeter accumulates bytes read and time spent reading. Embed it in a reader
// that meters its own source inline: call observe() in Read, stats() in Close.
// Unlike meteredReader it does not wrap anything, so there is no ambiguity about
// which object to read from.
type readMeter struct {
	bytes int64
	read  time.Duration
}

func (m *readMeter) observe(n int, since time.Time) {
	m.bytes += int64(n)
	m.read += time.Since(since)
}

// stats reports the meter as a ReadStats. Stored and delivered counts are equal —
// these readers don't decompress (decompressReader builds its own).
func (m *readMeter) stats() *ReadStats {
	return &ReadStats{
		StoredBytes:    m.bytes,
		DeliveredBytes: m.bytes,
		Read:           m.read,
	}
}

// rangeReader adapts an io.ReadCloser into a self-metering RangeReader.
type rangeReader struct {
	readMeter

	rc io.ReadCloser
}

func NewRangeReader(rc io.ReadCloser) RangeReader { return &rangeReader{rc: rc} }

func (r *rangeReader) Read(p []byte) (int, error) {
	t0 := time.Now()
	n, err := r.rc.Read(p)
	r.observe(n, t0)

	return n, err
}

func (r *rangeReader) Close(context.Context) (*ReadStats, error) {
	return r.stats(), r.rc.Close()
}

type sectionReader struct {
	readMeter

	sr   *io.SectionReader
	file *os.File
}

func newSectionReader(f *os.File, off, length int64) *sectionReader {
	return &sectionReader{
		sr:   io.NewSectionReader(f, off, length),
		file: f,
	}
}

func (r *sectionReader) Read(p []byte) (int, error) {
	t0 := time.Now()
	n, err := r.sr.Read(p)
	r.observe(n, t0)

	return n, err
}

func (r *sectionReader) Close(context.Context) (*ReadStats, error) {
	return r.stats(), r.file.Close()
}

// meteredReader meters reads pulled through it by a downstream consumer (a
// decoder in the decompress pipeline), where inline metering isn't possible
// because this reader isn't the one calling the source's Read.
type meteredReader struct {
	readMeter

	inner io.Reader
}

func (m *meteredReader) Read(p []byte) (int, error) {
	t0 := time.Now()
	n, err := m.inner.Read(p)
	m.observe(n, t0)

	return n, err
}

// captureReader tees every read byte into a buffer and hands the captured
// bytes to onClose on Close. Used by the compressed cache writeback path.
// drainOnClose=true reads inner to EOF on Close even if the caller above hasn't
// consumed everything — the compressed cache needs the full frame regardless
// of how many bytes the decoder happened to demand.
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

func (r *captureReader) Close(ctx context.Context) (*ReadStats, error) {
	if r.drainOnClose {
		_, _ = io.Copy(io.Discard, r)
	}
	stats, err := r.inner.Close(ctx)
	if err == nil {
		r.onClose(ctx, r.buf.Bytes())
	}

	return stats, err
}

// spanReader ends a trace span on Close, recording the close error or the first
// non-EOF read error against it. Stats pass through from inner, which (like every
// RangeReader) self-reports them.
type spanReader struct {
	inner   RangeReader
	span    trace.Span
	readErr error
}

func newSpanReader(inner RangeReader, span trace.Span) *spanReader {
	return &spanReader{inner: inner, span: span}
}

func (r *spanReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}

	return n, err
}

func (r *spanReader) Close(ctx context.Context) (*ReadStats, error) {
	stats, closeErr := r.inner.Close(ctx)

	if closeErr != nil {
		recordError(r.span, closeErr)
	} else if r.readErr != nil {
		recordError(r.span, r.readErr)
	}
	r.span.End()

	return stats, closeErr
}
