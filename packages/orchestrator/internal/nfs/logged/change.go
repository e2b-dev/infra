package logged

import (
	"context"
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type loggedChange struct {
	ctx   context.Context
	inner billy.Change
}

var _ billy.Change = (*loggedChange)(nil)

func newChange(ctx context.Context, change billy.Change) loggedChange {
	return loggedChange{ctx: ctx, inner: change}
}

func (s loggedChange) Chmod(name string, mode os.FileMode) (err error) {
	logStart(s.ctx, "Chmod")
	defer func() { logEndWithError(s.ctx, "Chmod", err) }()

	return s.inner.Chmod(name, mode)
}

func (s loggedChange) Lchown(name string, uid, gid int) (err error) {
	logStart(s.ctx, "Lchown")
	defer func() { logEndWithError(s.ctx, "Lchown", err) }()

	return s.inner.Lchown(name, uid, gid)
}

func (s loggedChange) Chown(name string, uid, gid int) (err error) {
	logStart(s.ctx, "Chown")
	defer func() { logEndWithError(s.ctx, "Chown", err) }()

	return s.inner.Chown(name, uid, gid)
}

func (s loggedChange) Chtimes(name string, atime time.Time, mtime time.Time) (err error) {
	logStart(s.ctx, "Chtimes")
	defer func() { logEndWithError(s.ctx, "Chtimes", err) }()

	return s.inner.Chtimes(name, atime, mtime)
}
