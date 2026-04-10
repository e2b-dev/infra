package middleware

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type wrappedHandler struct {
	inner        nfs.Handler
	interceptors *Chain
}

var _ nfs.Handler = (*wrappedHandler)(nil)

// WrapHandler wraps an nfs.Handler with the interceptor chain.
func WrapHandler(handler nfs.Handler, interceptors *Chain) nfs.Handler {
	return &wrappedHandler{inner: handler, interceptors: interceptors}
}

func (w *wrappedHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	var status nfs.MountStatus
	var fs billy.Filesystem
	var auth []nfs.AuthFlavor

	_ = w.interceptors.Exec(ctx, "Handler.Mount", []any{conn.RemoteAddr().String(), string(req.Dirpath)},
		func(ctx context.Context) error {
			status, fs, auth = w.inner.Mount(ctx, conn, req)
			if status != nfs.MountStatusOk {
				return fmt.Errorf("mount status: %d", status)
			}

			return nil
		})

	return status, WrapFilesystem(ctx, fs, w.interceptors), auth
}

func (w *wrappedHandler) Change(ctx context.Context, fs billy.Filesystem) (change billy.Change) {
	err := w.interceptors.Exec(ctx, "Handler.Change", nil,
		func(ctx context.Context) error {
			change = w.inner.Change(ctx, fs)

			return nil
		})
	if err != nil {
		return nil
	}

	return WrapChange(ctx, change, w.interceptors)
}

func (w *wrappedHandler) FSStat(ctx context.Context, fs billy.Filesystem, stat *nfs.FSStat) error {
	return w.interceptors.Exec(ctx, "Handler.FSStat", nil,
		func(ctx context.Context) error {
			return w.inner.FSStat(ctx, fs, stat)
		},
	)
}

func (w *wrappedHandler) ToHandle(ctx context.Context, fs billy.Filesystem, path []string) []byte {
	var result []byte

	_ = w.interceptors.Exec(ctx, "Handler.ToHandle", []any{path},
		func(ctx context.Context) error {
			result = w.inner.ToHandle(ctx, fs, path)

			return nil
		})

	return result
}

func (w *wrappedHandler) FromHandle(ctx context.Context, fh []byte) (billy.Filesystem, []string, error) {
	var fs billy.Filesystem
	var paths []string

	err := w.interceptors.Exec(ctx, "Handler.FromHandle", nil,
		func(ctx context.Context) error {
			var err error
			fs, paths, err = w.inner.FromHandle(ctx, fh)

			return err
		})

	// Note: We intentionally do NOT wrap the filesystem here.
	// The caching handler (inner) returns the already-wrapped filesystem
	// that was stored during ToHandle (which received the wrapped fs from Mount).
	// Wrapping again would cause double-interception of filesystem operations.
	return fs, paths, err
}

func (w *wrappedHandler) InvalidateHandle(ctx context.Context, fs billy.Filesystem, fh []byte) error {
	return w.interceptors.Exec(ctx, "Handler.InvalidateHandle", nil,
		func(ctx context.Context) error {
			return w.inner.InvalidateHandle(ctx, fs, fh)
		})
}

func (w *wrappedHandler) HandleLimit() int {
	return w.inner.HandleLimit()
}
