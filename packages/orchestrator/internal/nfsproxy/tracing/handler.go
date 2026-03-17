package tracing

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
	"go.opentelemetry.io/otel/attribute"
)

type tracingHandler struct {
	inner nfs.Handler
}

var _ nfs.Handler = (*tracingHandler)(nil)

func WrapWithTracing(handler nfs.Handler) nfs.Handler {
	return &tracingHandler{inner: handler}
}

func (e *tracingHandler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (s nfs.MountStatus, fs billy.Filesystem, auth []nfs.AuthFlavor) {
	ctx, finish := startSpan(ctx, "NFS.Mount",
		attribute.String("net.conn.remote_addr", conn.RemoteAddr().String()),
		attribute.String("nfs.mount.dirpath", string(request.Dirpath)))

	defer func() {
		var err error
		if s != nfs.MountStatusOk {
			err = fmt.Errorf("mount status = %d", s)
		}
		finish(err)
	}()

	s, fs, auth = e.inner.Mount(ctx, conn, request)
	if fs != nil {
		fs = newFS(ctx, fs)
	}

	return
}

func (e *tracingHandler) Change(filesystem billy.Filesystem) billy.Change {
	// billy.Filesystem should already be wrapped by newFS, so it should be a tracingFS
	// but we don't have a context here in the signature.
	// We'll try to get it from the filesystem if it's our wrapper.
	ctx := context.Background()
	if tfs, ok := filesystem.(*tracingFS); ok {
		ctx = tfs.ctx
	}

	_, finish := startSpan(ctx, "NFS.Change")
	defer finish(nil)

	change := e.inner.Change(filesystem)

	return newChange(ctx, change)
}

func (e *tracingHandler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) (err error) {
	ctx, finish := startSpan(ctx, "NFS.FSStat")
	defer func() { finish(err) }()

	return e.inner.FSStat(ctx, filesystem, stat)
}

func (e *tracingHandler) ToHandle(fs billy.Filesystem, path []string) (fh []byte) {
	ctx := context.Background()
	if tfs, ok := fs.(*tracingFS); ok {
		ctx = tfs.ctx
	}

	_, finish := startSpan(ctx, "NFS.ToHandle", attribute.StringSlice("nfs.path", path))
	defer finish(nil)

	return e.inner.ToHandle(fs, path)
}

func (e *tracingHandler) FromHandle(fh []byte) (fs billy.Filesystem, paths []string, err error) {
	// FromHandle doesn't take a context, and we don't have a filesystem yet.
	// This is a bit tricky for tracing if we want to link it to a parent span.
	// For now, we'll use Background.
	ctx, finish := startSpan(context.Background(), "NFS.FromHandle")
	defer func() { finish(err) }()

	fs, paths, err = e.inner.FromHandle(fh)
	if fs != nil {
		fs = newFS(ctx, fs)
	}

	return
}

func (e *tracingHandler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) (err error) {
	ctx := context.Background()
	if tfs, ok := filesystem.(*tracingFS); ok {
		ctx = tfs.ctx
	}

	_, finish := startSpan(ctx, "NFS.InvalidateHandle")
	defer func() { finish(err) }()

	return e.inner.InvalidateHandle(filesystem, bytes)
}

func (e *tracingHandler) HandleLimit() int {
	return e.inner.HandleLimit()
}
