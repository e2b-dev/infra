package recovery

import (
	"context"

	"github.com/go-git/go-billy/v5"
)

type file struct {
	inner billy.File
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
}

var _ billy.File = (*file)(nil)

func wrapFile(ctx context.Context, f billy.File) billy.File {
	if f == nil {
		return nil
	}

	return &file{inner: f, ctx: ctx}
}

func (f *file) Name() string {
	defer tryRecovery(f.ctx, "File.Name")

	return f.inner.Name()
}

func (f *file) Write(p []byte) (n int, e error) {
	defer deferErrRecovery(f.ctx, "File.Write", &e)

	return f.inner.Write(p)
}

func (f *file) Read(p []byte) (n int, e error) {
	defer deferErrRecovery(f.ctx, "File.Read", &e)

	return f.inner.Read(p)
}

func (f *file) ReadAt(p []byte, off int64) (n int, e error) {
	defer deferErrRecovery(f.ctx, "File.ReadAt", &e)

	return f.inner.ReadAt(p, off)
}

func (f *file) Seek(offset int64, whence int) (n int64, e error) {
	defer deferErrRecovery(f.ctx, "File.Seek", &e)

	return f.inner.Seek(offset, whence)
}

func (f *file) Close() (e error) {
	defer deferErrRecovery(f.ctx, "File.Close", &e)

	return f.inner.Close()
}

func (f *file) Lock() (e error) {
	defer deferErrRecovery(f.ctx, "File.Lock", &e)

	return f.inner.Lock()
}

func (f *file) Unlock() (e error) {
	defer deferErrRecovery(f.ctx, "File.Unlock", &e)

	return f.inner.Unlock()
}

func (f *file) Truncate(size int64) (e error) {
	defer deferErrRecovery(f.ctx, "File.Truncate", &e)

	return f.inner.Truncate(size)
}
