package logged

import (
	"context"
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type loggedChange struct {
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
	inner billy.Change
}

var _ billy.Change = (*loggedChange)(nil)

func newChange(ctx context.Context, change billy.Change) loggedChange {
	return loggedChange{ctx: ctx, inner: change}
}

func (s loggedChange) Chmod(name string, mode os.FileMode) (err error) {
	finish := logStart(s.ctx, "Change.Chmod", name, mode)
	defer func() { finish(s.ctx, err) }()

	return s.inner.Chmod(name, mode)
}

func (s loggedChange) Lchown(name string, uid, gid int) (err error) {
	finish := logStart(s.ctx, "Change.Lchown", name, uid, gid)
	defer func() { finish(s.ctx, err) }()

	return s.inner.Lchown(name, uid, gid)
}

func (s loggedChange) Chown(name string, uid, gid int) (err error) {
	finish := logStart(s.ctx, "Change.Chown", name, uid, gid)
	defer func() { finish(s.ctx, err) }()

	return s.inner.Chown(name, uid, gid)
}

func (s loggedChange) Chtimes(name string, atime time.Time, mtime time.Time) (err error) {
	finish := logStart(s.ctx, "Change.Chtimes", name, atime, mtime)
	defer func() { finish(s.ctx, err) }()

	return s.inner.Chtimes(name, atime, mtime)
}
