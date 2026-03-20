package metrics

import (
	"context"

	"github.com/go-git/go-billy/v5"
)

type metricsFile struct {
	ctx        context.Context //nolint:containedctx
	inner      billy.File
	finishOpen finishFunc
}

var _ billy.File = (*metricsFile)(nil)

func wrapFile(ctx context.Context, f billy.File, finish finishFunc) billy.File {
	return &metricsFile{ctx: ctx, inner: f, finishOpen: finish}
}

func (m *metricsFile) Name() string {
	return m.inner.Name()
}

func (m *metricsFile) Write(p []byte) (n int, err error) {
	finish := recordCall(m.ctx, "File.Write")
	defer func() { finish(err) }()

	return m.inner.Write(p)
}

func (m *metricsFile) Read(p []byte) (n int, err error) {
	finish := recordCall(m.ctx, "File.Read")
	defer func() { finish(err) }()

	return m.inner.Read(p)
}

func (m *metricsFile) ReadAt(p []byte, off int64) (n int, err error) {
	finish := recordCall(m.ctx, "File.ReadAt")
	defer func() { finish(err) }()

	return m.inner.ReadAt(p, off)
}

func (m *metricsFile) Seek(offset int64, whence int) (n int64, err error) {
	finish := recordCall(m.ctx, "File.Seek")
	defer func() { finish(err) }()

	return m.inner.Seek(offset, whence)
}

func (m *metricsFile) Close() (err error) {
	finish := recordCall(m.ctx, "File.Close")
	defer func() {
		finish(err)
		// End the open metric when the file is closed
		if m.finishOpen != nil {
			m.finishOpen(err)
		}
	}()

	return m.inner.Close()
}

func (m *metricsFile) Lock() (err error) {
	finish := recordCall(m.ctx, "File.Lock")
	defer func() { finish(err) }()

	return m.inner.Lock()
}

func (m *metricsFile) Unlock() (err error) {
	finish := recordCall(m.ctx, "File.Unlock")
	defer func() { finish(err) }()

	return m.inner.Unlock()
}

func (m *metricsFile) Truncate(size int64) (err error) {
	finish := recordCall(m.ctx, "File.Truncate")
	defer func() { finish(err) }()

	return m.inner.Truncate(size)
}
