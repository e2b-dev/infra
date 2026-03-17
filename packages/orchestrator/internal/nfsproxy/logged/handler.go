package logged

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type loggedHandler struct {
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
	inner nfs.Handler
}

var _ nfs.Handler = (*loggedHandler)(nil)

var setLogLevelOnce sync.Once

func WrapWithLogging(ctx context.Context, handler nfs.Handler) nfs.Handler {
	setLogLevelOnce.Do(func() {
		nfs.Log.SetLevel(nfs.TraceLevel)
	})

	return loggedHandler{ctx: ctx, inner: handler}
}

func (e loggedHandler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (s nfs.MountStatus, fs billy.Filesystem, auth []nfs.AuthFlavor) {
	finish := logStart(ctx, "Handler.Mount",
		fmt.Sprintf("net.Conn{LocalAddr=%q, RemoteAddr=%q}", conn.LocalAddr(), conn.RemoteAddr()),
		fmt.Sprintf("nfs.MountRequest{Dirpath=%q}", string(request.Dirpath)))
	defer func() {
		var err error
		if s != nfs.MountStatusOk {
			err = fmt.Errorf("mount status = %d", s)
		}
		finish(ctx, err)
	}()

	s, fs, auth = e.inner.Mount(ctx, conn, request)
	fs = newFS(ctx, fs)

	return
}

func (e loggedHandler) Change(ctx context.Context, filesystem billy.Filesystem) billy.Change {
	finish := logStart(ctx, "Handler.Change")
	defer finish(ctx, nil)

	change := e.inner.Change(ctx, filesystem)

	return newChange(ctx, change)
}

func (e loggedHandler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) (err error) {
	finish := logStart(ctx, "Handler.FSStat")
	defer func() { finish(ctx, err) }()

	return e.inner.FSStat(ctx, filesystem, stat)
}

func (e loggedHandler) ToHandle(ctx context.Context, fs billy.Filesystem, path []string) (fh []byte) {
	finish := logStart(ctx, "Handler.ToHandle", path)
	defer func() { finish(ctx, nil, fh) }()

	return e.inner.ToHandle(ctx, fs, path)
}

func (e loggedHandler) FromHandle(ctx context.Context, fh []byte) (fs billy.Filesystem, paths []string, err error) {
	finish := logStart(ctx, "Handler.FromHandle", fh)
	defer func() { finish(ctx, err, paths) }()

	return e.inner.FromHandle(ctx, fh)
}

func (e loggedHandler) InvalidateHandle(ctx context.Context, filesystem billy.Filesystem, bytes []byte) (err error) {
	finish := logStart(ctx, "Handler.InvalidateHandle")
	defer func() { finish(ctx, err) }()

	return e.inner.InvalidateHandle(ctx, filesystem, bytes)
}

func (e loggedHandler) HandleLimit() int {
	finish := logStart(e.ctx, "Handler.HandleLimit")
	defer finish(e.ctx, nil)

	return e.inner.HandleLimit()
}
