package metrics

import (
	"context"
	"os"

	"github.com/go-git/go-billy/v5"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/cfg"
)

type metricsFS struct {
	ctx   context.Context //nolint:containedctx
	inner billy.Filesystem

	config cfg.Config
}

var _ billy.Filesystem = (*metricsFS)(nil)

func wrapFS(ctx context.Context, fs billy.Filesystem, config cfg.Config) billy.Filesystem {
	return &metricsFS{ctx: ctx, inner: fs, config: config}
}

func (m *metricsFS) Create(filename string) (f billy.File, err error) {
	finish := recordCall(m.ctx, "FS.Create")

	f, err = m.inner.Create(filename)
	if err != nil {
		finish(err)

		return
	}

	f = wrapFile(m.ctx, f, finish)

	return
}

func (m *metricsFS) Open(filename string) (f billy.File, err error) {
	finish := recordCall(m.ctx, "FS.Open")

	f, err = m.inner.Open(filename)
	if err != nil {
		finish(err)

		return
	}

	f = wrapFile(m.ctx, f, finish)

	return
}

func (m *metricsFS) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, err error) {
	finish := recordCall(m.ctx, "FS.OpenFile")

	f, err = m.inner.OpenFile(filename, flag, perm)
	if err != nil {
		finish(err)

		return
	}

	f = wrapFile(m.ctx, f, finish)

	return
}

func (m *metricsFS) Stat(filename string) (fi os.FileInfo, err error) {
	if !m.config.RecordStatCalls {
		return m.inner.Stat(filename)
	}

	finish := recordCall(m.ctx, "FS.Stat")
	defer func() { finish(err) }()

	return m.inner.Stat(filename)
}

func (m *metricsFS) Rename(oldpath, newpath string) (err error) {
	finish := recordCall(m.ctx, "FS.Rename")
	defer func() { finish(err) }()

	return m.inner.Rename(oldpath, newpath)
}

func (m *metricsFS) Remove(filename string) (err error) {
	finish := recordCall(m.ctx, "FS.Remove")
	defer func() { finish(err) }()

	return m.inner.Remove(filename)
}

func (m *metricsFS) Join(elem ...string) string {
	return m.inner.Join(elem...)
}

func (m *metricsFS) TempFile(dir, prefix string) (f billy.File, err error) {
	finish := recordCall(m.ctx, "FS.TempFile")

	f, err = m.inner.TempFile(dir, prefix)
	if err != nil {
		finish(err)

		return
	}

	f = wrapFile(m.ctx, f, finish)

	return
}

func (m *metricsFS) ReadDir(path string) (fi []os.FileInfo, err error) {
	finish := recordCall(m.ctx, "FS.ReadDir")
	defer func() { finish(err) }()

	return m.inner.ReadDir(path)
}

func (m *metricsFS) MkdirAll(filename string, perm os.FileMode) (err error) {
	finish := recordCall(m.ctx, "FS.MkdirAll")
	defer func() { finish(err) }()

	return m.inner.MkdirAll(filename, perm)
}

func (m *metricsFS) Lstat(filename string) (fi os.FileInfo, err error) {
	if !m.config.RecordStatCalls {
		return m.inner.Lstat(filename)
	}

	finish := recordCall(m.ctx, "FS.Lstat")
	defer func() { finish(err) }()

	return m.inner.Lstat(filename)
}

func (m *metricsFS) Symlink(target, link string) (err error) {
	finish := recordCall(m.ctx, "FS.Symlink")
	defer func() { finish(err) }()

	return m.inner.Symlink(target, link)
}

func (m *metricsFS) Readlink(link string) (target string, err error) {
	finish := recordCall(m.ctx, "FS.Readlink")
	defer func() { finish(err) }()

	return m.inner.Readlink(link)
}

func (m *metricsFS) Chroot(path string) (fs billy.Filesystem, err error) {
	finish := recordCall(m.ctx, "FS.Chroot")
	defer func() { finish(err) }()

	inner, err := m.inner.Chroot(path)
	if err != nil {
		return nil, err
	}

	return wrapFS(m.ctx, inner, m.config), nil
}

func (m *metricsFS) Root() string {
	return m.inner.Root()
}

func (m *metricsFS) Unwrap() billy.Filesystem {
	return m.inner
}
