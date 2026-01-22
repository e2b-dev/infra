package jailed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/helper/chroot"
	"github.com/willscott/go-nfs"
)

var ErrInvalidSandbox = errors.New("invalid sandbox")

var _ billy.Filesystem = (*mountFailedFS)(nil)

type GetPrefix func(context.Context, net.Conn, nfs.MountRequest) (string, error)

type Handler struct {
	getPrefix GetPrefix
	inner     nfs.Handler
}

func (h Handler) String() string {
	return fmt.Sprintf("Handler{inner=%v}", h.inner)
}

var _ nfs.Handler = (*Handler)(nil)

func NewNFSHandler(inner nfs.Handler, prefix GetPrefix) Handler {
	return Handler{inner: inner, getPrefix: prefix}
}

func (h Handler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	teamID, err := h.getPrefix(ctx, conn, request)
	if err != nil {
		slog.Warn("failed to get prefix", "error", err)

		return nfs.MountStatusErrAcces, mountFailedFS{}, nil
	}

	volumeName := string(request.Dirpath)
	dirPath := filepath.Join(teamID, volumeName)

	status, fs, auth := h.inner.Mount(ctx, conn, request)
	if err = fs.MkdirAll(dirPath, 0o755); err != nil {
		slog.Error("failed to create jail cell", "error", err)

		return nfs.MountStatusErrIO, nil, nil
	}

	fs = chroot.New(fs, dirPath)

	return status, fs, auth
}

func (h Handler) Change(filesystem billy.Filesystem) billy.Change {
	return h.inner.Change(filesystem)
}

func (h Handler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) error {
	return h.inner.FSStat(ctx, filesystem, stat)
}

func (h Handler) ToHandle(fs billy.Filesystem, path []string) []byte {
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
