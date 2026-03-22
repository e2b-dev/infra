package metrics

import (
	"context"
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type metricsChange struct {
	ctx   context.Context //nolint:containedctx
	inner billy.Change
}

var _ billy.Change = (*metricsChange)(nil)

func newChange(ctx context.Context, change billy.Change) billy.Change {
	return &metricsChange{ctx: ctx, inner: change}
}

func (m *metricsChange) Chmod(name string, mode os.FileMode) (err error) {
	finish := recordCall(m.ctx, "Change.Chmod")
	defer func() { finish(err) }()

	return m.inner.Chmod(name, mode)
}

func (m *metricsChange) Lchown(name string, uid, gid int) (err error) {
	finish := recordCall(m.ctx, "Change.Lchown")
	defer func() { finish(err) }()

	return m.inner.Lchown(name, uid, gid)
}

func (m *metricsChange) Chown(name string, uid, gid int) (err error) {
	finish := recordCall(m.ctx, "Change.Chown")
	defer func() { finish(err) }()

	return m.inner.Chown(name, uid, gid)
}

func (m *metricsChange) Chtimes(name string, atime time.Time, mtime time.Time) (err error) {
	finish := recordCall(m.ctx, "Change.Chtimes")
	defer func() { finish(err) }()

	return m.inner.Chtimes(name, atime, mtime)
}
