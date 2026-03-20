package tracing

import (
	"context"

	"github.com/go-git/go-billy/v5"
	"go.opentelemetry.io/otel/attribute"
)

type tracingFile struct {
	ctx        context.Context //nolint:containedctx
	inner      billy.File
	finishOpen finishFunc
}

var _ billy.File = (*tracingFile)(nil)

func wrapFile(ctx context.Context, f billy.File, finish finishFunc) billy.File {
	return &tracingFile{ctx: ctx, inner: f, finishOpen: finish}
}

func (l *tracingFile) Name() string {
	return l.inner.Name()
}

func (l *tracingFile) Write(p []byte) (n int, err error) {
	_, finish := startSpan(l.ctx, "File.Write", attribute.Int("nfs.len", len(p)))
	defer func() { finish(err, attribute.Int("nfs.n", n)) }()

	return l.inner.Write(p)
}

func (l *tracingFile) Read(p []byte) (n int, err error) {
	_, finish := startSpan(l.ctx, "File.Read", attribute.Int("nfs.len", len(p)))
	defer func() { finish(err, attribute.Int("nfs.n", n)) }()

	return l.inner.Read(p)
}

func (l *tracingFile) ReadAt(p []byte, off int64) (n int, err error) {
	_, finish := startSpan(l.ctx, "File.ReadAt",
		attribute.Int("nfs.len", len(p)),
		attribute.Int64("nfs.offset", off))
	defer func() { finish(err, attribute.Int("nfs.n", n)) }()

	return l.inner.ReadAt(p, off)
}

func (l *tracingFile) Seek(offset int64, whence int) (n int64, err error) {
	_, finish := startSpan(l.ctx, "File.Seek",
		attribute.Int64("nfs.offset", offset),
		attribute.Int("nfs.whence", whence))
	defer func() { finish(err, attribute.Int64("nfs.n", n)) }()

	return l.inner.Seek(offset, whence)
}

func (l *tracingFile) Close() (err error) {
	_, finish := startSpan(l.ctx, "File.Close")
	defer func() {
		finish(err)
		// End the open span when the file is closed
		if l.finishOpen != nil {
			l.finishOpen(err)
		}
	}()

	return l.inner.Close()
}

func (l *tracingFile) Lock() (err error) {
	_, finish := startSpan(l.ctx, "File.Lock")
	defer func() { finish(err) }()

	return l.inner.Lock()
}

func (l *tracingFile) Unlock() (err error) {
	_, finish := startSpan(l.ctx, "File.Unlock")
	defer func() { finish(err) }()

	return l.inner.Unlock()
}

func (l *tracingFile) Truncate(size int64) (err error) {
	_, finish := startSpan(l.ctx, "File.Truncate", attribute.Int64("nfs.size", size))
	defer func() { finish(err) }()

	return l.inner.Truncate(size)
}
