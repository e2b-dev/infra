package tracing

import (
	"context"
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
	"go.opentelemetry.io/otel/attribute"
)

type tracingChange struct {
	ctx   context.Context //nolint:containedctx
	inner billy.Change
}

var _ billy.Change = (*tracingChange)(nil)

func newChange(ctx context.Context, change billy.Change) billy.Change {
	return &tracingChange{ctx: ctx, inner: change}
}

func (l *tracingChange) Chmod(name string, mode os.FileMode) (err error) {
	_, finish := startSpan(l.ctx, "Change.Chmod",
		attribute.String("nfs.name", name),
		attribute.String("nfs.mode", mode.String()))
	defer func() { finish(err) }()

	return l.inner.Chmod(name, mode)
}

func (l *tracingChange) Lchown(name string, uid, gid int) (err error) {
	_, finish := startSpan(l.ctx, "Change.Lchown",
		attribute.String("nfs.name", name),
		attribute.Int("nfs.uid", uid),
		attribute.Int("nfs.gid", gid))
	defer func() { finish(err) }()

	return l.inner.Lchown(name, uid, gid)
}

func (l *tracingChange) Chown(name string, uid, gid int) (err error) {
	_, finish := startSpan(l.ctx, "Change.Chown",
		attribute.String("nfs.name", name),
		attribute.Int("nfs.uid", uid),
		attribute.Int("nfs.gid", gid))
	defer func() { finish(err) }()

	return l.inner.Chown(name, uid, gid)
}

func (l *tracingChange) Chtimes(name string, atime time.Time, mtime time.Time) (err error) {
	_, finish := startSpan(l.ctx, "Change.Chtimes",
		attribute.String("nfs.name", name),
		attribute.String("nfs.atime", atime.String()),
		attribute.String("nfs.mtime", mtime.String()))
	defer func() { finish(err) }()

	return l.inner.Chtimes(name, atime, mtime)
}
