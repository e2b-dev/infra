package recovery

import (
	"context"
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type change struct {
	inner billy.Change
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
}

var _ billy.Change = (*change)(nil)

func wrapChange(ctx context.Context, c billy.Change) billy.Change {
	if c == nil {
		return nil
	}

	return &change{inner: c, ctx: ctx}
}

func (c *change) Chmod(name string, mode os.FileMode) (e error) {
	defer deferErrRecovery(c.ctx, "Change.Chmod", &e)

	return c.inner.Chmod(name, mode)
}

func (c *change) Lchown(name string, uid, gid int) (e error) {
	defer deferErrRecovery(c.ctx, "Change.Lchown", &e)

	return c.inner.Lchown(name, uid, gid)
}

func (c *change) Chown(name string, uid, gid int) (e error) {
	defer deferErrRecovery(c.ctx, "Change.Chown", &e)

	return c.inner.Chown(name, uid, gid)
}

func (c *change) Chtimes(name string, atime time.Time, mtime time.Time) (e error) {
	defer deferErrRecovery(c.ctx, "Change.Chtimes", &e)

	return c.inner.Chtimes(name, atime, mtime)
}
