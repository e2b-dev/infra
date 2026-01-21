package jailed

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

var ErrInvalidSandbox = errors.New("invalid sandbox")

type mountFailedFS struct{}

func (m mountFailedFS) Create(filename string) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Open(filename string) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Stat(filename string) (os.FileInfo, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Rename(oldpath, newpath string) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Remove(filename string) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Join(elem ...string) string {
	return strings.Join(elem, "/")
}

func (m mountFailedFS) TempFile(dir, prefix string) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) ReadDir(path string) ([]os.FileInfo, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) MkdirAll(filename string, perm os.FileMode) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Lstat(filename string) (os.FileInfo, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Symlink(target, link string) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Readlink(link string) (string, error) {
	return "", ErrInvalidSandbox
}

func (m mountFailedFS) Chroot(path string) (billy.Filesystem, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Root() string {
	return ""
}

var _ billy.Filesystem = (*mountFailedFS)(nil)

type GetPrefix func(context.Context, net.Conn, nfs.MountRequest) (string, error)

type Handler struct {
	getPrefix GetPrefix
	inner     nfs.Handler
}

var _ nfs.Handler = (*Handler)(nil)

func NewNFSHandler(inner nfs.Handler, prefix GetPrefix) Handler {
	return Handler{inner: inner, getPrefix: prefix}
}

func (h Handler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	prefix, err := h.getPrefix(ctx, conn, request)
	if err != nil {
		slog.Warn("failed to get prefix", "error", err)

		return nfs.MountStatusErrAcces, mountFailedFS{}, nil
	}

	dirPath := string(request.Dirpath)
	dirPath = filepath.Join(prefix, dirPath)
	request.Dirpath = []byte(dirPath)

	status, fs, auth := h.inner.Mount(ctx, conn, request)
	if err = fs.MkdirAll(dirPath, 0o755); err != nil {
		slog.Error("failed to create jail cell", "error", err)

		return nfs.MountStatusErrIO, nil, nil
	}

	return status, tryWrapFS(fs, prefix), auth
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
