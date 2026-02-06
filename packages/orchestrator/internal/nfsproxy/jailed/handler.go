package jailed

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

var ErrInvalidSandbox = errors.New("invalid sandbox")

type GetPrefix func(context.Context, net.Addr, nfs.MountRequest) (billy.Filesystem, string, error)

type GetChange func(billy.Filesystem) billy.Change

type Handler struct {
	getPrefix GetPrefix
	getChange GetChange
}

func (h Handler) String() string {
	return "jailed.Handler{}"
}

var _ nfs.Handler = (*Handler)(nil)

func NewNFSHandler(prefix GetPrefix, getChange GetChange) Handler {
	return Handler{getPrefix: prefix, getChange: getChange}
}

func (h Handler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	fs, prefix, err := h.getPrefix(ctx, conn.RemoteAddr(), request)
	if err != nil {
		slog.Warn("failed to get prefix", "error", err)

		return nfs.MountStatusErrAcces, mountFailedFS{}, nil
	}

	dirPath := string(request.Dirpath)
	dirPath = filepath.Join(prefix, dirPath)
	request.Dirpath = []byte(dirPath)

	return nfs.MountStatusOk, tryWrapFS(fs, prefix), nil
}

func (h Handler) Change(fs billy.Filesystem) billy.Change {
	return h.getChange(fs)
}

func (h Handler) FSStat(_ context.Context, _ billy.Filesystem, _ *nfs.FSStat) error {
	return nil // todo: fill out fields on nfs.FSStat
}

func (h Handler) ToHandle(_ billy.Filesystem, _ []string) []byte {
	panic("this should be intercepted by the caching handler")
}

func (h Handler) FromHandle(_ []byte) (billy.Filesystem, []string, error) {
	panic("this should be intercepted by the caching handler")
}

func (h Handler) InvalidateHandle(_ billy.Filesystem, _ []byte) error {
	panic("this should be intercepted by the caching handler")
}

func (h Handler) HandleLimit() int {
	panic("this should be intercepted by the caching handler")
}
