package logged

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type loggedHandler struct {
	ctx   context.Context
	inner nfs.Handler
}

var _ nfs.Handler = (*loggedHandler)(nil)

func NewHandler(ctx context.Context, handler nfs.Handler) nfs.Handler {
	nfs.Log.SetLevel(nfs.TraceLevel)

	return loggedHandler{ctx: ctx, inner: handler}
}

func (e loggedHandler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (s nfs.MountStatus, fs billy.Filesystem, auth []nfs.AuthFlavor) {
	logStart(ctx, "Handler.Mount")
	defer func() {
		var err error
		if s != nfs.MountStatusOk {
			err = fmt.Errorf("mount status = %d", s)
		}
		logEndWithError(ctx, "Handler.Mount", err)
	}()

	s, fs, auth = e.inner.Mount(ctx, conn, request)
	fs = newFS(ctx, fs)

	return
}

func (e loggedHandler) Change(filesystem billy.Filesystem) billy.Change {
	logStart(e.ctx, "Handler.Change")
	defer logEnd(e.ctx, "Handler.Change")

	change := e.inner.Change(filesystem)

	return newChange(e.ctx, change)
}

func (e loggedHandler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) (err error) {
	logStart(ctx, "Handler.FSStat")
	defer func() { logEndWithError(ctx, "Handler.FSStat", err) }()

	return e.inner.FSStat(ctx, filesystem, stat)
}

func (e loggedHandler) ToHandle(fs billy.Filesystem, path []string) (fh []byte) {
	logStart(e.ctx, "Handler.ToHandle", path)
	defer func() { logEnd(e.ctx, "Handler.ToHandle", fh) }()

	return e.inner.ToHandle(fs, path)
}

func (e loggedHandler) FromHandle(fh []byte) (fs billy.Filesystem, paths []string, err error) {
	logStart(e.ctx, "Handler.FromHandle", fh)
	defer func() { logEndWithError(e.ctx, "Handler.FromHandle", err, paths) }()

	return e.inner.FromHandle(fh)
}

func (e loggedHandler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) (err error) {
	logStart(e.ctx, "Handler.InvalidateHandle")
	defer func() { logEndWithError(e.ctx, "Handler.InvalidateHandle", err) }()

	return e.inner.InvalidateHandle(filesystem, bytes)
}

func (e loggedHandler) HandleLimit() int {
	logStart(e.ctx, "Handler.HandleLimit")
	defer logEnd(e.ctx, "Handler.HandleLimit")

	return e.inner.HandleLimit()
}
