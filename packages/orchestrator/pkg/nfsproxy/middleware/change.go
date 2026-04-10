package middleware

import (
	"context"
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type wrappedChange struct {
	inner billy.Change
	chain *Chain
	ctx   context.Context //nolint:containedctx
}

var _ billy.Change = (*wrappedChange)(nil)

// WrapChange wraps a billy.Change with the interceptor chain.
func WrapChange(ctx context.Context, c billy.Change, chain *Chain) billy.Change {
	if c == nil {
		return nil
	}

	return &wrappedChange{inner: c, chain: chain, ctx: ctx}
}

func (w *wrappedChange) Chmod(name string, mode os.FileMode) error {
	return w.chain.Exec(w.ctx, "Change.Chmod", []any{name, mode},
		func(_ context.Context) error {
			return w.inner.Chmod(name, mode)
		})
}

func (w *wrappedChange) Lchown(name string, uid, gid int) error {
	return w.chain.Exec(w.ctx, "Change.Lchown", []any{name, uid, gid},
		func(_ context.Context) error {
			return w.inner.Lchown(name, uid, gid)
		})
}

func (w *wrappedChange) Chown(name string, uid, gid int) error {
	return w.chain.Exec(w.ctx, "Change.Chown", []any{name, uid, gid},
		func(_ context.Context) error {
			return w.inner.Chown(name, uid, gid)
		})
}

func (w *wrappedChange) Chtimes(name string, atime, mtime time.Time) error {
	return w.chain.Exec(w.ctx, "Change.Chtimes", []any{name, atime, mtime},
		func(_ context.Context) error {
			return w.inner.Chtimes(name, atime, mtime)
		})
}
