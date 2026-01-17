package jailed

import (
	"context"
	"log/slog"
	"net"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type GetPrefix func(net.Conn) (string, error)

type Handler struct {
	getPrefix GetPrefix
	inner     nfs.Handler
}

var _ nfs.Handler = (*Handler)(nil)

func NewNFSHandler(inner nfs.Handler, prefix GetPrefix) Handler {
	return Handler{inner: inner, getPrefix: prefix}
}

func (h Handler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	prefix, err := h.getPrefix(conn)
	if err != nil {
		slog.Warn("failed to get prefix", "error", err)
		return nfs.MountStatusErrIO, nil, nil
	}

	dirPath := string(request.Dirpath)
	dirPath = filepath.Join(prefix, dirPath)
	request.Dirpath = []byte(dirPath)

	status, fs, auth := h.inner.Mount(ctx, conn, request)
	if err = fs.MkdirAll(dirPath, 0755); err != nil {
		slog.Error("failed to create jail cell", "error", err)
		return nfs.MountStatusErrIO, nil, nil
	}

	return status, wrapFS(fs, prefix), auth
}

func (h Handler) Change(filesystem billy.Filesystem) billy.Change {
	change := h.inner.Change(filesystem)
	return wrapChange(change)
}

func (h Handler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) error {
	return h.inner.FSStat(ctx, filesystem, stat)
}

func (h Handler) ToHandle(fs billy.Filesystem, path []string) []byte {
	jfs, ok := h.findJailedFS(fs)
	if ok && jfs.needsPrefix(path) {
		path = append([]string{jfs.prefix}, path...)
	}

	return h.inner.ToHandle(fs, path)
}

func (h Handler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	return h.inner.FromHandle(fh)
}

func (h Handler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) error {
	return h.inner.InvalidateHandle(filesystem, bytes)
}

func (h Handler) HandleLimit() int {
	return h.inner.HandleLimit()
}

type unwrappable interface {
	Unwrap() billy.Filesystem
}

func (h Handler) findJailedFS(fs billy.Filesystem) (jailedFS, bool) {
	for {
		if jfs, ok := fs.(jailedFS); ok {
			return jfs, true
		}

		if wfs, ok := fs.(unwrappable); ok {
			fs = wfs.Unwrap()
			continue
		}

		return jailedFS{}, false
	}
}
