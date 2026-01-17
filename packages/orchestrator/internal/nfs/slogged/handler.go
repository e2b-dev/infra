package slogged

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type loggedHandler struct {
	inner nfs.Handler
}

var _ nfs.Handler = (*loggedHandler)(nil)

func NewHandler(handler nfs.Handler) nfs.Handler {
	return loggedHandler{inner: handler}
}

func (e loggedHandler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (s nfs.MountStatus, fs billy.Filesystem, auth []nfs.AuthFlavor) {
	slogStart("Handler.Mount")
	defer func() {
		var err error
		if s != nfs.MountStatusOk {
			err = fmt.Errorf("mount status = %d", s)
		}
		slogEndWithError("Handler.Mount", err)
	}()

	s, fs, auth = e.inner.Mount(ctx, conn, request)
	fs = newFS(fs)
	return
}

func (e loggedHandler) Change(filesystem billy.Filesystem) billy.Change {
	slogStart("Handler.Change")
	defer slogEnd("Handler.Change")

	change := e.inner.Change(filesystem)
	return newChange(change)
}

func (e loggedHandler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) (err error) {
	slogStart("Handler.FSStat")
	defer func() { slogEndWithError("Handler.FSStat", err) }()

	return e.inner.FSStat(ctx, filesystem, stat)
}

func (e loggedHandler) ToHandle(fs billy.Filesystem, path []string) (fh []byte) {
	slogStart("Handler.ToHandle", path)
	defer func() { slogEnd("Handler.ToHandle", fh) }()

	return e.inner.ToHandle(fs, path)
}

func (e loggedHandler) FromHandle(fh []byte) (fs billy.Filesystem, paths []string, err error) {
	slogStart("Handler.FromHandle", fh)
	defer func() { slogEndWithError("Handler.FromHandle", err, paths) }()

	return e.inner.FromHandle(fh)
}

func (e loggedHandler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) (err error) {
	slogStart("Handler.InvalidateHandle")
	defer func() { slogEndWithError("Handler.InvalidateHandle", err) }()

	return e.inner.InvalidateHandle(filesystem, bytes)
}

func (e loggedHandler) HandleLimit() int {
	slogStart("Handler.HandleLimit")
	defer slogEnd("Handler.HandleLimit")

	return e.inner.HandleLimit()
}
