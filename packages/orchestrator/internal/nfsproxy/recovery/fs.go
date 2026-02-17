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

func (fs *filesystem) Create(filename string) (f billy.File, e error) {
	defer deferErrRecovery(fs.ctx, "FS.Create", &e)
	f, e = fs.inner.Create(filename)

	return wrapFile(fs.ctx, f), e
}

func (fs *filesystem) Open(filename string) (f billy.File, e error) {
	defer deferErrRecovery(fs.ctx, "FS.Open", &e)
	f, e = fs.inner.Open(filename)

	return wrapFile(fs.ctx, f), e
}

func (fs *filesystem) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, e error) {
	defer deferErrRecovery(fs.ctx, "FS.OpenFile", &e)
	f, e = fs.inner.OpenFile(filename, flag, perm)

	return wrapFile(fs.ctx, f), e
}

func (fs *filesystem) Stat(filename string) (fi os.FileInfo, e error) {
	defer deferErrRecovery(fs.ctx, "FS.Stat", &e)

	return fs.inner.Stat(filename)
}

func (fs *filesystem) Rename(oldpath, newpath string) (e error) {
	defer deferErrRecovery(fs.ctx, "FS.Rename", &e)

	return fs.inner.Rename(oldpath, newpath)
}

func (fs *filesystem) Remove(filename string) (e error) {
	defer deferErrRecovery(fs.ctx, "FS.Remove", &e)

	return fs.inner.Remove(filename)
}

func (fs *filesystem) Join(elem ...string) string {
	defer tryRecovery(fs.ctx, "Join")

	return fs.inner.Join(elem...)
}

func (fs *filesystem) TempFile(dir, prefix string) (f billy.File, e error) {
	defer deferErrRecovery(fs.ctx, "FS.TempFile", &e)
	f, e = fs.inner.TempFile(dir, prefix)

	return wrapFile(fs.ctx, f), e
}

func (fs *filesystem) ReadDir(path string) (fis []os.FileInfo, e error) {
	defer deferErrRecovery(fs.ctx, "FS.ReadDir", &e)

	return fs.inner.ReadDir(path)
}

func (fs *filesystem) MkdirAll(filename string, perm os.FileMode) (e error) {
	defer deferErrRecovery(fs.ctx, "FS.MkdirAll", &e)

	return fs.inner.MkdirAll(filename, perm)
}

func (fs *filesystem) Lstat(filename string) (fi os.FileInfo, e error) {
	defer deferErrRecovery(fs.ctx, "FS.Lstat", &e)

	return fs.inner.Lstat(filename)
}

func (fs *filesystem) Symlink(target, link string) (e error) {
	defer deferErrRecovery(fs.ctx, "FS.Symlink", &e)

	return fs.inner.Symlink(target, link)
}

func (fs *filesystem) Readlink(link string) (s string, e error) {
	defer deferErrRecovery(fs.ctx, "FS.Readlink", &e)

	return fs.inner.Readlink(link)
}

func (fs *filesystem) Chroot(path string) (f billy.Filesystem, e error) {
	defer deferErrRecovery(fs.ctx, "FS.Chroot", &e)
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
