package logged

import (
	"context"
	"os"

	"github.com/go-git/go-billy/v5"
)

type loggedFS struct {
	ctx   context.Context
	inner billy.Filesystem
}

var _ billy.Filesystem = (*loggedFS)(nil)

func newFS(ctx context.Context, fs billy.Filesystem) loggedFS {
	return loggedFS{ctx: ctx, inner: fs}
}

func (l loggedFS) Unwrap() billy.Filesystem {
	return l.inner
}

func (l loggedFS) Create(filename string) (f billy.File, err error) {
	logStart(l.ctx, "FS.Create", filename)
	defer func() { logEndWithError(l.ctx, "FS.Create", err) }()

	f, err = l.inner.Create(filename)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) Open(filename string) (f billy.File, err error) {
	logStart(l.ctx, "FS.Open", filename)
	defer func() { logEndWithError(l.ctx, "FS.Open", err) }()

	f, err = l.inner.Open(filename)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, err error) {
	logStart(l.ctx, "FS.OpenFile", filename, flag, perm)
	defer func() { logEndWithError(l.ctx, "FS.OpenFile", err) }()

	f, err = l.inner.OpenFile(filename, flag, perm)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) Stat(filename string) (fi os.FileInfo, err error) {
	logStart(l.ctx, "FS.Stat", filename)
	defer func() { logEndWithError(l.ctx, "FS.Stat", err) }()

	return l.inner.Stat(filename)
}

func (l loggedFS) Rename(oldpath, newpath string) (err error) {
	logStart(l.ctx, "FS.Rename")
	defer func() { logEndWithError(l.ctx, "FS.Rename", err) }()

	return l.inner.Rename(oldpath, newpath)
}

func (l loggedFS) Remove(filename string) (err error) {
	logStart(l.ctx, "FS.Remove")
	defer func() { logEndWithError(l.ctx, "FS.Remove", err) }()

	return l.inner.Remove(filename)
}

func (l loggedFS) Join(elem ...string) (path string) {
	logStart(l.ctx, "FS.Join", elem)
	defer func() { logEnd(l.ctx, "FS.Join", path) }()

	return l.inner.Join(elem...)
}

func (l loggedFS) TempFile(dir, prefix string) (f billy.File, err error) {
	logStart(l.ctx, "FS.TempFile")
	defer func() { logEndWithError(l.ctx, "FS.TempFile", err) }()

	f, err = l.inner.TempFile(dir, prefix)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) ReadDir(path string) (fi []os.FileInfo, err error) {
	logStart(l.ctx, "FS.ReadDir", path)
	defer func() { logEndWithError(l.ctx, "FS.ReadDir", err) }()

	return l.inner.ReadDir(path)
}

func (l loggedFS) MkdirAll(filename string, perm os.FileMode) (err error) {
	logStart(l.ctx, "FS.MkdirAll")
	defer func() { logEndWithError(l.ctx, "FS.MkdirAll", err) }()

	return l.inner.MkdirAll(filename, perm)
}

func (l loggedFS) Lstat(filename string) (fi os.FileInfo, err error) {
	logStart(l.ctx, "FS.Lstat", filename)
	defer func() { logEndWithError(l.ctx, "FS.Lstat", err) }()

	return l.inner.Lstat(filename)
}

func (l loggedFS) Symlink(target, link string) (err error) {
	logStart(l.ctx, "FS.Symlink")
	defer func() { logEndWithError(l.ctx, "FS.Symlink", err) }()

	return l.inner.Symlink(target, link)
}

func (l loggedFS) Readlink(link string) (target string, err error) {
	logStart(l.ctx, "FS.Readlink")
	defer func() { logEndWithError(l.ctx, "FS.Readlink", err) }()

	return l.inner.Readlink(link)
}

func (l loggedFS) Chroot(path string) (fs billy.Filesystem, err error) {
	logStart(l.ctx, "FS.Chroot")
	defer func() { logEndWithError(l.ctx, "FS.Chroot", err) }()

	inner, err := l.inner.Chroot(path)
	if err != nil {
		return nil, err
	}

	return newFS(l.ctx, inner), nil
}

func (l loggedFS) Root() string {
	return l.inner.Root()
}
