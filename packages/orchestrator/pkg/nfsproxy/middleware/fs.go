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

func (w *wrappedFS) Create(filename string) (f billy.File, err error) {
	err = w.chain.Exec(w.ctx, FSCreateRequest{Filename: filename},
		func(_ context.Context) error {
			f, err = w.inner.Create(filename)

			return err
		})

	return WrapFile(w.ctx, f, w.chain), err
}

func (w *wrappedFS) Open(filename string) (f billy.File, err error) {
	err = w.chain.Exec(w.ctx, FSOpenRequest{Filename: filename},
		func(_ context.Context) error {
			f, err = w.inner.Open(filename)

			return err
		})

	return WrapFile(w.ctx, f, w.chain), err
}

func (w *wrappedFS) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, err error) {
	err = w.chain.Exec(w.ctx, FSOpenFileRequest{Filename: filename, Flag: flag, Perm: perm},
		func(_ context.Context) error {
			f, err = w.inner.OpenFile(filename, flag, perm)

			return err
		})

	return WrapFile(w.ctx, f, w.chain), err
}

func (w *wrappedFS) Stat(filename string) (info os.FileInfo, err error) {
	err = w.chain.Exec(w.ctx, FSStatRequest{Filename: filename},
		func(_ context.Context) error {
			info, err = w.inner.Stat(filename)

			return err
		})

	return info, err
}

func (w *wrappedFS) Rename(oldpath, newpath string) error {
	return w.chain.Exec(w.ctx, FSRenameRequest{OldPath: oldpath, NewPath: newpath},
		func(_ context.Context) error {
			return w.inner.Rename(oldpath, newpath)
		})
}

func (w *wrappedFS) Remove(filename string) error {
	return w.chain.Exec(w.ctx, FSRemoveRequest{Filename: filename},
		func(_ context.Context) error {
			return w.inner.Remove(filename)
		})
}

func (w *wrappedFS) Join(elem ...string) string {
	return w.inner.Join(elem...)
}

func (w *wrappedFS) TempFile(dir, prefix string) (f billy.File, err error) {
	err = w.chain.Exec(w.ctx, FSTempFileRequest{Dir: dir, Prefix: prefix},
		func(_ context.Context) error {
			f, err = w.inner.TempFile(dir, prefix)

			return err
		})

	return WrapFile(w.ctx, f, w.chain), err
}

func (w *wrappedFS) ReadDir(path string) (infos []os.FileInfo, err error) {
	err = w.chain.Exec(w.ctx, FSReadDirRequest{Path: path},
		func(_ context.Context) error {
			infos, err = w.inner.ReadDir(path)

			return err
		})

	return infos, err
}

func (w *wrappedFS) MkdirAll(filename string, perm os.FileMode) error {
	return w.chain.Exec(w.ctx, FSMkdirAllRequest{Filename: filename, Perm: perm},
		func(_ context.Context) error {
			return w.inner.MkdirAll(filename, perm)
		})
}

func (w *wrappedFS) Lstat(filename string) (info os.FileInfo, err error) {
	err = w.chain.Exec(w.ctx, FSLstatRequest{Filename: filename},
		func(_ context.Context) error {
			info, err = w.inner.Lstat(filename)

			return err
		})

	return info, err
}

func (w *wrappedFS) Symlink(target, link string) error {
	return w.chain.Exec(w.ctx, FSSymlinkRequest{Target: target, Link: link},
		func(_ context.Context) error {
			return w.inner.Symlink(target, link)
		})
}

func (w *wrappedFS) Readlink(link string) (target string, err error) {
	err = w.chain.Exec(w.ctx, FSReadlinkRequest{Link: link},
		func(_ context.Context) error {
			target, err = w.inner.Readlink(link)

			return err
		})

	return target, err
}

func (w *wrappedFS) Chroot(path string) (fs billy.Filesystem, err error) {
	err = w.chain.Exec(w.ctx, FSChrootRequest{Path: path},
		func(_ context.Context) error {
			fs, err = w.inner.Chroot(path)

			return err
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
