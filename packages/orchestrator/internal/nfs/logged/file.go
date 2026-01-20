package logged

import (
	"context"

	"github.com/go-git/go-billy/v5"
)

type loggedFile struct {
	ctx   context.Context
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
	logStart(l.ctx, "File.Write", len(p))
	defer func() { logEndWithError(l.ctx, "File.Write", err, n) }()

	return l.inner.Write(p)
}

func (l loggedFile) Read(p []byte) (n int, err error) {
	logStart(l.ctx, "File.Read", len(p))
	defer func() { logEndWithError(l.ctx, "File.Read", err, n) }()

	return l.inner.Read(p)
}

func (l loggedFile) ReadAt(p []byte, off int64) (n int, err error) {
	logStart(l.ctx, "File.ReadAt", len(p), off)
	defer func() { logEndWithError(l.ctx, "File.ReadAt", err, n) }()

	return l.inner.ReadAt(p, off)
}

func (l loggedFile) Seek(offset int64, whence int) (n int64, err error) {
	logStart(l.ctx, "File.Seek", offset, whence)
	defer func() { logEndWithError(l.ctx, "File.Seek", err, n) }()

	return l.inner.Seek(offset, whence)
}

func (l loggedFile) Close() (err error) {
	logStart(l.ctx, "File.Close")
	defer func() { logEndWithError(l.ctx, "File.Close", err) }()

	return l.inner.Close()
}

func (l loggedFile) Lock() (err error) {
	logStart(l.ctx, "File.Lock")
	defer func() { logEndWithError(l.ctx, "File.Lock", err) }()

	return l.inner.Lock()
}

func (l loggedFile) Unlock() (err error) {
	logStart(l.ctx, "File.Unlock")
	defer func() { logEndWithError(l.ctx, "File.Unlock", err) }()

	return l.inner.Unlock()
}

func (l loggedFile) Truncate(size int64) (err error) {
	logStart(l.ctx, "File.Truncate", size)
	defer func() { logEndWithError(l.ctx, "File.Truncate", err) }()

	return l.inner.Truncate(size)
}
