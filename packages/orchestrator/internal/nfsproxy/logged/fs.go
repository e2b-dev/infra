package logged

import (
	"context"
	"os"

	"github.com/go-git/go-billy/v5"
)

type loggedFS struct {
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
	inner billy.Filesystem
}

var _ billy.Filesystem = (*loggedFS)(nil)

func newFS(ctx context.Context, fs billy.Filesystem) loggedFS {
	return loggedFS{ctx: ctx, inner: fs}
}

func (l loggedFS) Create(filename string) (f billy.File, err error) {
	finish := logStart(l.ctx, "FS.Create", filename)
	defer func() { finish(l.ctx, err, f) }()

	f, err = l.inner.Create(filename)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) Open(filename string) (f billy.File, err error) {
	finish := logStart(l.ctx, "FS.Open", filename)
	defer func() { finish(l.ctx, err, f) }()

	f, err = l.inner.Open(filename)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, err error) {
	finish := logStart(l.ctx, "FS.OpenFile", filename, flag, perm)
	defer func() { finish(l.ctx, err, f) }()

	f, err = l.inner.OpenFile(filename, flag, perm)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) Stat(filename string) (fi os.FileInfo, err error) {
	finish := logStart(l.ctx, "FS.Stat", filename)
	defer func() { finish(l.ctx, err, fi) }()

	return l.inner.Stat(filename)
}

func (l loggedFS) Rename(oldpath, newpath string) (err error) {
	finish := logStart(l.ctx, "FS.Rename", oldpath, newpath)
	defer func() { finish(l.ctx, err) }()

	return l.inner.Rename(oldpath, newpath)
}

func (l loggedFS) Remove(filename string) (err error) {
	finish := logStart(l.ctx, "FS.Remove", filename)
	defer func() { finish(l.ctx, err) }()

	return l.inner.Remove(filename)
}

func (l loggedFS) Join(elem ...string) (path string) {
	finish := logStart(l.ctx, "FS.Join", elem)
	defer func() { finish(l.ctx, nil, path) }()

	return l.inner.Join(elem...)
}

func (l loggedFS) TempFile(dir, prefix string) (f billy.File, err error) {
	finish := logStart(l.ctx, "FS.TempFile", dir, prefix)
	defer func() { finish(l.ctx, err, f) }()

	f, err = l.inner.TempFile(dir, prefix)
	f = wrapFile(l.ctx, f)

	return
}

func (l loggedFS) ReadDir(path string) (fi []os.FileInfo, err error) {
	finish := logStart(l.ctx, "FS.ReadDir", path)
	defer func() { finish(l.ctx, err, fi) }()

	return l.inner.ReadDir(path)
}

func (l loggedFS) MkdirAll(filename string, perm os.FileMode) (err error) {
	finish := logStart(l.ctx, "FS.MkdirAll", filename, perm)
	defer func() { finish(l.ctx, err) }()

	return l.inner.MkdirAll(filename, perm)
}

func (l loggedFS) Lstat(filename string) (fi os.FileInfo, err error) {
	finish := logStart(l.ctx, "FS.Lstat", filename)
	defer func() { finish(l.ctx, err, fi) }()

	return l.inner.Lstat(filename)
}

func (l loggedFS) Symlink(target, link string) (err error) {
	finish := logStart(l.ctx, "FS.Symlink", target, link)
	defer func() { finish(l.ctx, err) }()

	return l.inner.Symlink(target, link)
}

func (l loggedFS) Readlink(link string) (target string, err error) {
	finish := logStart(l.ctx, "FS.Readlink", link)
	defer func() { finish(l.ctx, err, target) }()

	return l.inner.Readlink(link)
}

func (l loggedFS) Chroot(path string) (fs billy.Filesystem, err error) {
	finish := logStart(l.ctx, "FS.Chroot", path)
	defer func() { finish(l.ctx, err, fs) }()

	inner, err := l.inner.Chroot(path)
	if err != nil {
		return nil, err
	}

	return newFS(l.ctx, inner), nil
}

func (l loggedFS) Root() string {
	return l.inner.Root()
}
