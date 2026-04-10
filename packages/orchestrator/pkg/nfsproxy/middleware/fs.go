package middleware

import (
	"context"
	"os"

	"github.com/go-git/go-billy/v5"
)

type wrappedFS struct {
	inner billy.Filesystem
	chain *Chain
	ctx   context.Context //nolint:containedctx
}

var _ billy.Filesystem = (*wrappedFS)(nil)

// WrapFilesystem wraps a billy.Filesystem with the interceptor chain.
func WrapFilesystem(ctx context.Context, fs billy.Filesystem, chain *Chain) billy.Filesystem {
	if fs == nil {
		return nil
	}

	return &wrappedFS{inner: fs, chain: chain, ctx: ctx}
}

func (w *wrappedFS) Create(filename string) (billy.File, error) {
	var file billy.File
	_, err := w.chain.Exec(w.ctx, "FS.Create", []any{filename},
		func(_ context.Context) ([]any, error) {
			var err error
			file, err = w.inner.Create(filename)

			return []any{file}, err
		})

	return WrapFile(w.ctx, file, w.chain), err
}

func (w *wrappedFS) Open(filename string) (billy.File, error) {
	var file billy.File
	_, err := w.chain.Exec(w.ctx, "FS.Open", []any{filename},
		func(_ context.Context) ([]any, error) {
			var err error
			file, err = w.inner.Open(filename)

			return []any{file}, err
		})

	return WrapFile(w.ctx, file, w.chain), err
}

func (w *wrappedFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	var file billy.File
	_, err := w.chain.Exec(w.ctx, "FS.OpenFile", []any{filename, flag, perm},
		func(_ context.Context) ([]any, error) {
			var err error
			file, err = w.inner.OpenFile(filename, flag, perm)

			return []any{file}, err
		})

	return WrapFile(w.ctx, file, w.chain), err
}

func (w *wrappedFS) Stat(filename string) (os.FileInfo, error) {
	var info os.FileInfo
	_, err := w.chain.Exec(w.ctx, "FS.Stat", []any{filename},
		func(_ context.Context) ([]any, error) {
			var err error
			info, err = w.inner.Stat(filename)

			return []any{info}, err
		})

	return info, err
}

func (w *wrappedFS) Rename(oldpath, newpath string) error {
	_, err := w.chain.Exec(w.ctx, "FS.Rename", []any{oldpath, newpath},
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Rename(oldpath, newpath)
		})

	return err
}

func (w *wrappedFS) Remove(filename string) error {
	_, err := w.chain.Exec(w.ctx, "FS.Remove", []any{filename},
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Remove(filename)
		})

	return err
}

func (w *wrappedFS) Join(elem ...string) string {
	return w.inner.Join(elem...)
}

func (w *wrappedFS) TempFile(dir, prefix string) (billy.File, error) {
	var file billy.File
	_, err := w.chain.Exec(w.ctx, "FS.TempFile", []any{dir, prefix},
		func(_ context.Context) ([]any, error) {
			var err error
			file, err = w.inner.TempFile(dir, prefix)

			return []any{file}, err
		})

	return WrapFile(w.ctx, file, w.chain), err
}

func (w *wrappedFS) ReadDir(path string) ([]os.FileInfo, error) {
	var infos []os.FileInfo
	_, err := w.chain.Exec(w.ctx, "FS.ReadDir", []any{path},
		func(_ context.Context) ([]any, error) {
			var err error
			infos, err = w.inner.ReadDir(path)

			return []any{infos}, err
		})

	return infos, err
}

func (w *wrappedFS) MkdirAll(filename string, perm os.FileMode) error {
	_, err := w.chain.Exec(w.ctx, "FS.MkdirAll", []any{filename, perm},
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.MkdirAll(filename, perm)
		})

	return err
}

func (w *wrappedFS) Lstat(filename string) (os.FileInfo, error) {
	var info os.FileInfo
	_, err := w.chain.Exec(w.ctx, "FS.Lstat", []any{filename},
		func(_ context.Context) ([]any, error) {
			var err error
			info, err = w.inner.Lstat(filename)

			return []any{info}, err
		})

	return info, err
}

func (w *wrappedFS) Symlink(target, link string) error {
	_, err := w.chain.Exec(w.ctx, "FS.Symlink", []any{target, link},
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Symlink(target, link)
		})

	return err
}

func (w *wrappedFS) Readlink(link string) (string, error) {
	var target string
	_, err := w.chain.Exec(w.ctx, "FS.Readlink", []any{link},
		func(_ context.Context) ([]any, error) {
			var err error
			target, err = w.inner.Readlink(link)

			return []any{target}, err
		})

	return target, err
}

func (w *wrappedFS) Chroot(path string) (billy.Filesystem, error) {
	var fs billy.Filesystem
	_, err := w.chain.Exec(w.ctx, "FS.Chroot", []any{path},
		func(_ context.Context) ([]any, error) {
			var err error
			fs, err = w.inner.Chroot(path)

			return []any{fs}, err
		})

	return WrapFilesystem(w.ctx, fs, w.chain), err
}

func (w *wrappedFS) Root() string {
	return w.inner.Root()
}

// Unwrap returns the inner filesystem (used by go-nfs internals).
func (w *wrappedFS) Unwrap() billy.Filesystem {
	return w.inner
}
