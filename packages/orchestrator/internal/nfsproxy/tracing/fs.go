package tracing

import (
	"context"
	"os"

	"github.com/go-git/go-billy/v5"
	"go.opentelemetry.io/otel/attribute"
)

type tracingFS struct {
	ctx   context.Context //nolint:containedctx
	inner billy.Filesystem
}

var _ billy.Filesystem = (*tracingFS)(nil)

func newFS(ctx context.Context, fs billy.Filesystem) billy.Filesystem {
	return &tracingFS{ctx: ctx, inner: fs}
}

func (l *tracingFS) Create(filename string) (f billy.File, err error) {
	ctx, finish := startSpan(l.ctx, "FS.Create", attribute.String("nfs.filename", filename))

	f, err = l.inner.Create(filename)
	if f != nil {
		f = wrapFile(ctx, f, finish)
	} else {
		finish(err)
	}

	return
}

func (l *tracingFS) Open(filename string) (f billy.File, err error) {
	ctx, finish := startSpan(l.ctx, "FS.Open", attribute.String("nfs.filename", filename))

	f, err = l.inner.Open(filename)
	if f != nil {
		f = wrapFile(ctx, f, finish)
	} else {
		finish(err)
	}

	return
}

func (l *tracingFS) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, err error) {
	ctx, finish := startSpan(l.ctx, "FS.OpenFile",
		attribute.String("nfs.filename", filename),
		attribute.Int("nfs.flag", flag),
		attribute.String("nfs.perm", perm.String()))

	f, err = l.inner.OpenFile(filename, flag, perm)
	if f != nil {
		f = wrapFile(ctx, f, finish)
	} else {
		finish(err)
	}

	return
}

func (l *tracingFS) Stat(filename string) (fi os.FileInfo, err error) {
	_, finish := startSpan(l.ctx, "FS.Stat", attribute.String("nfs.filename", filename))
	defer func() { finish(err) }()

	return l.inner.Stat(filename)
}

func (l *tracingFS) Rename(oldpath, newpath string) (err error) {
	_, finish := startSpan(l.ctx, "FS.Rename",
		attribute.String("nfs.oldpath", oldpath),
		attribute.String("nfs.newpath", newpath))
	defer func() { finish(err) }()

	return l.inner.Rename(oldpath, newpath)
}

func (l *tracingFS) Remove(filename string) (err error) {
	_, finish := startSpan(l.ctx, "FS.Remove", attribute.String("nfs.filename", filename))
	defer func() { finish(err) }()

	return l.inner.Remove(filename)
}

func (l *tracingFS) Join(elem ...string) string {
	return l.inner.Join(elem...)
}

func (l *tracingFS) TempFile(dir, prefix string) (f billy.File, err error) {
	ctx, finish := startSpan(l.ctx, "FS.TempFile",
		attribute.String("nfs.dir", dir),
		attribute.String("nfs.prefix", prefix))

	f, err = l.inner.TempFile(dir, prefix)
	if f != nil {
		f = wrapFile(ctx, f, finish)
	} else {
		finish(err)
	}

	return
}

func (l *tracingFS) ReadDir(path string) (fi []os.FileInfo, err error) {
	_, finish := startSpan(l.ctx, "FS.ReadDir", attribute.String("nfs.path", path))
	defer func() { finish(err) }()

	return l.inner.ReadDir(path)
}

func (l *tracingFS) MkdirAll(filename string, perm os.FileMode) (err error) {
	_, finish := startSpan(l.ctx, "FS.MkdirAll",
		attribute.String("nfs.filename", filename),
		attribute.String("nfs.perm", perm.String()))
	defer func() { finish(err) }()

	return l.inner.MkdirAll(filename, perm)
}

func (l *tracingFS) Lstat(filename string) (fi os.FileInfo, err error) {
	_, finish := startSpan(l.ctx, "FS.Lstat", attribute.String("nfs.filename", filename))
	defer func() { finish(err) }()

	return l.inner.Lstat(filename)
}

func (l *tracingFS) Symlink(target, link string) (err error) {
	_, finish := startSpan(l.ctx, "FS.Symlink",
		attribute.String("nfs.target", target),
		attribute.String("nfs.link", link))
	defer func() { finish(err) }()

	return l.inner.Symlink(target, link)
}

func (l *tracingFS) Readlink(link string) (target string, err error) {
	_, finish := startSpan(l.ctx, "FS.Readlink", attribute.String("nfs.link", link))
	defer func() { finish(err) }()

	return l.inner.Readlink(link)
}

func (l *tracingFS) Chroot(path string) (fs billy.Filesystem, err error) {
	ctx, finish := startSpan(l.ctx, "FS.Chroot", attribute.String("nfs.path", path))
	defer func() { finish(err) }()

	inner, err := l.inner.Chroot(path)
	if err != nil {
		return nil, err
	}

	return newFS(ctx, inner), nil
}

func (l *tracingFS) Root() string {
	return l.inner.Root()
}

func (l *tracingFS) Unwrap() billy.Filesystem {
	return l.inner
}
