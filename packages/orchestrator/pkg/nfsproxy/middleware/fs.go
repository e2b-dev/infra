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
	results, err := w.chain.Exec(w.ctx, "FS.Create", []any{filename},
		func(_ context.Context) ([]any, error) {
			f, err := w.inner.Create(filename)

			return []any{f}, err
		})
	if err != nil {
		return nil, err
	}

	return WrapFile(w.ctx, results[0].(billy.File), w.chain), nil
}

func (w *wrappedFS) Open(filename string) (billy.File, error) {
	results, err := w.chain.Exec(w.ctx, "FS.Open", []any{filename},
		func(_ context.Context) ([]any, error) {
			f, err := w.inner.Open(filename)

			return []any{f}, err
		})
	if err != nil {
		return nil, err
	}

	return WrapFile(w.ctx, results[0].(billy.File), w.chain), nil
}

func (w *wrappedFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	results, err := w.chain.Exec(w.ctx, "FS.OpenFile", []any{filename, flag, perm},
		func(_ context.Context) ([]any, error) {
			f, err := w.inner.OpenFile(filename, flag, perm)

			return []any{f}, err
		})
	if err != nil {
		return nil, err
	}

	return WrapFile(w.ctx, results[0].(billy.File), w.chain), nil
}

func (w *wrappedFS) Stat(filename string) (os.FileInfo, error) {
	results, err := w.chain.Exec(w.ctx, "FS.Stat", []any{filename},
		func(_ context.Context) ([]any, error) {
			info, err := w.inner.Stat(filename)

			return []any{info}, err
		})
	if err != nil {
		return nil, err
	}

	return results[0].(os.FileInfo), nil
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
	results, err := w.chain.Exec(w.ctx, "FS.TempFile", []any{dir, prefix},
		func(_ context.Context) ([]any, error) {
			f, err := w.inner.TempFile(dir, prefix)

			return []any{f}, err
		})
	if err != nil {
		return nil, err
	}

	return WrapFile(w.ctx, results[0].(billy.File), w.chain), nil
}

func (w *wrappedFS) ReadDir(path string) ([]os.FileInfo, error) {
	results, err := w.chain.Exec(w.ctx, "FS.ReadDir", []any{path},
		func(_ context.Context) ([]any, error) {
			infos, err := w.inner.ReadDir(path)

			return []any{infos}, err
		})
	if err != nil {
		return nil, err
	}

	return results[0].([]os.FileInfo), nil
}

func (w *wrappedFS) MkdirAll(filename string, perm os.FileMode) error {
	_, err := w.chain.Exec(w.ctx, "FS.MkdirAll", []any{filename, perm},
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.MkdirAll(filename, perm)
		})

	return err
}

func (w *wrappedFS) Lstat(filename string) (os.FileInfo, error) {
	results, err := w.chain.Exec(w.ctx, "FS.Lstat", []any{filename},
		func(_ context.Context) ([]any, error) {
			info, err := w.inner.Lstat(filename)

			return []any{info}, err
		})
	if err != nil {
		return nil, err
	}

	return results[0].(os.FileInfo), nil
}

func (w *wrappedFS) Symlink(target, link string) error {
	_, err := w.chain.Exec(w.ctx, "FS.Symlink", []any{target, link},
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Symlink(target, link)
		})

	return err
}

func (w *wrappedFS) Readlink(link string) (string, error) {
	results, err := w.chain.Exec(w.ctx, "FS.Readlink", []any{link},
		func(_ context.Context) ([]any, error) {
			target, err := w.inner.Readlink(link)

			return []any{target}, err
		})
	if err != nil {
		return "", err
	}

	return results[0].(string), nil
}

func (w *wrappedFS) Chroot(path string) (billy.Filesystem, error) {
	results, err := w.chain.Exec(w.ctx, "FS.Chroot", []any{path},
		func(_ context.Context) ([]any, error) {
			fs, err := w.inner.Chroot(path)

			return []any{fs}, err
		})
	if err != nil {
		return nil, err
	}

	return WrapFilesystem(w.ctx, results[0].(billy.Filesystem), w.chain), nil
}

func (w *wrappedFS) Root() string {
	return w.inner.Root()
}

// Unwrap returns the inner filesystem (used by go-nfs internals).
func (w *wrappedFS) Unwrap() billy.Filesystem {
	return w.inner
}
