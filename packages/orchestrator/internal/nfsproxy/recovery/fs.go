package recovery

import (
	"context"
	"os"

	"github.com/go-git/go-billy/v5"
)

type filesystem struct {
	inner billy.Filesystem
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
}

var _ billy.Filesystem = (*filesystem)(nil)

func wrapFS(ctx context.Context, fs billy.Filesystem) billy.Filesystem {
	if fs == nil {
		return nil
	}

	return &filesystem{inner: fs, ctx: ctx}
}

func (fs *filesystem) Create(filename string) (billy.File, error) {
	defer tryRecovery(fs.ctx, "Create")
	file, err := fs.inner.Create(filename)

	return wrapFile(fs.ctx, file), err
}

func (fs *filesystem) Open(filename string) (billy.File, error) {
	defer tryRecovery(fs.ctx, "Open")
	file, err := fs.inner.Open(filename)

	return wrapFile(fs.ctx, file), err
}

func (fs *filesystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	defer tryRecovery(fs.ctx, "OpenFile")
	file, err := fs.inner.OpenFile(filename, flag, perm)

	return wrapFile(fs.ctx, file), err
}

func (fs *filesystem) Stat(filename string) (os.FileInfo, error) {
	defer tryRecovery(fs.ctx, "Stat")

	return fs.inner.Stat(filename)
}

func (fs *filesystem) Rename(oldpath, newpath string) error {
	defer tryRecovery(fs.ctx, "Rename")

	return fs.inner.Rename(oldpath, newpath)
}

func (fs *filesystem) Remove(filename string) error {
	defer tryRecovery(fs.ctx, "Remove")

	return fs.inner.Remove(filename)
}

func (fs *filesystem) Join(elem ...string) string {
	defer tryRecovery(fs.ctx, "Join")

	return fs.inner.Join(elem...)
}

func (fs *filesystem) TempFile(dir, prefix string) (billy.File, error) {
	defer tryRecovery(fs.ctx, "TempFile")
	file, err := fs.inner.TempFile(dir, prefix)

	return wrapFile(fs.ctx, file), err
}

func (fs *filesystem) ReadDir(path string) ([]os.FileInfo, error) {
	defer tryRecovery(fs.ctx, "ReadDir")

	return fs.inner.ReadDir(path)
}

func (fs *filesystem) MkdirAll(filename string, perm os.FileMode) error {
	defer tryRecovery(fs.ctx, "MkdirAll")

	return fs.inner.MkdirAll(filename, perm)
}

func (fs *filesystem) Lstat(filename string) (os.FileInfo, error) {
	defer tryRecovery(fs.ctx, "Lstat")

	return fs.inner.Lstat(filename)
}

func (fs *filesystem) Symlink(target, link string) error {
	defer tryRecovery(fs.ctx, "Symlink")

	return fs.inner.Symlink(target, link)
}

func (fs *filesystem) Readlink(link string) (string, error) {
	defer tryRecovery(fs.ctx, "Readlink")

	return fs.inner.Readlink(link)
}

func (fs *filesystem) Chroot(path string) (billy.Filesystem, error) {
	defer tryRecovery(fs.ctx, "Chroot")
	inner, err := fs.inner.Chroot(path)
	if err != nil {
		return nil, err
	}

	return wrapFS(fs.ctx, inner), nil
}

func (fs *filesystem) Root() string {
	defer tryRecovery(fs.ctx, "Root")

	return fs.inner.Root()
}
