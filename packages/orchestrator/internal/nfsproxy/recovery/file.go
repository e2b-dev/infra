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

func (f *file) Write(p []byte) (int, error) {
	defer tryRecovery(f.ctx, "File.Write")

	return f.inner.Write(p)
}

func (f *file) Read(p []byte) (int, error) {
	defer tryRecovery(f.ctx, "File.Read")

	return f.inner.Read(p)
}

func (f *file) ReadAt(p []byte, off int64) (int, error) {
	defer tryRecovery(f.ctx, "File.ReadAt")

	return f.inner.ReadAt(p, off)
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	defer tryRecovery(f.ctx, "File.Seek")

	return f.inner.Seek(offset, whence)
}

func (f *file) Close() error {
	defer tryRecovery(f.ctx, "File.Close")

	return f.inner.Close()
}

func (f *file) Lock() error {
	defer tryRecovery(f.ctx, "File.Lock")

	return f.inner.Lock()
}

func (f *file) Unlock() error {
	defer tryRecovery(f.ctx, "File.Unlock")

	return f.inner.Unlock()
}

func (f *file) Truncate(size int64) error {
	defer tryRecovery(f.ctx, "File.Truncate")

	return f.inner.Truncate(size)
}
