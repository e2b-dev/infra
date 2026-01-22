package logged

import (
	"context"

	"github.com/go-git/go-billy/v5"
)

type loggedFile struct {
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
	inner billy.File
}

var _ billy.File = (*loggedFile)(nil)

func wrapFile(ctx context.Context, f billy.File) billy.File {
	return &loggedFile{ctx: ctx, inner: f}
}

func (l loggedFile) Name() string {
	return l.inner.Name()
}

func (l loggedFile) Write(p []byte) (n int, err error) {
	finish := logStart(l.ctx, "File.Write", len(p))
	defer func() { finish(l.ctx, err, n) }()

	return l.inner.Write(p)
}

func (l loggedFile) Read(p []byte) (n int, err error) {
	finish := logStart(l.ctx, "File.Read", len(p))
	defer func() { finish(l.ctx, err, n) }()

	return l.inner.Read(p)
}

func (l loggedFile) ReadAt(p []byte, off int64) (n int, err error) {
	finish := logStart(l.ctx, "File.ReadAt", len(p), off)
	defer func() { finish(l.ctx, err, n) }()

	return l.inner.ReadAt(p, off)
}

func (l loggedFile) Seek(offset int64, whence int) (n int64, err error) {
	finish := logStart(l.ctx, "File.Seek", offset, whence)
	defer func() { finish(l.ctx, err, n) }()

	return l.inner.Seek(offset, whence)
}

func (l loggedFile) Close() (err error) {
	finish := logStart(l.ctx, "File.Close")
	defer func() { finish(l.ctx, err) }()

	return l.inner.Close()
}

func (l loggedFile) Lock() (err error) {
	finish := logStart(l.ctx, "File.Lock")
	defer func() { finish(l.ctx, err) }()

	return l.inner.Lock()
}

func (l loggedFile) Unlock() (err error) {
	finish := logStart(l.ctx, "File.Unlock")
	defer func() { finish(l.ctx, err) }()

	return l.inner.Unlock()
}

func (l loggedFile) Truncate(size int64) (err error) {
	finish := logStart(l.ctx, "File.Truncate", size)
	defer func() { finish(l.ctx, err) }()

	return l.inner.Truncate(size)
}
