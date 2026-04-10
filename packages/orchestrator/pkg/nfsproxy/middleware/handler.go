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

	_, _ = w.interceptors.Exec(ctx, "Handler.Mount", []any{conn.RemoteAddr().String(), string(req.Dirpath)},
		func(ctx context.Context) ([]any, error) {
			status, fs, auth = w.inner.Mount(ctx, conn, req)
			var err error
			if status != nfs.MountStatusOk {
				err = fmt.Errorf("mount status: %d", status)
			}

			return []any{status, fs, auth}, err
		})

	return status, WrapFilesystem(ctx, fs, w.interceptors), auth
}

func (w *wrappedHandler) Change(ctx context.Context, fs billy.Filesystem) billy.Change {
	// Unwrap to get the inner filesystem for the handler
	if wrapped, ok := fs.(*wrappedFS); ok {
		fs = wrapped.inner
	}

	change := w.inner.Change(ctx, fs)

	return WrapChange(ctx, change, w.interceptors)
}

func (w *wrappedHandler) FSStat(ctx context.Context, fs billy.Filesystem, stat *nfs.FSStat) error {
	if wrapped, ok := fs.(*wrappedFS); ok {
		fs = wrapped.inner
	}

	_, err := w.interceptors.Exec(ctx, "Handler.FSStat", nil,
		func(ctx context.Context) ([]any, error) {
			return nil, w.inner.FSStat(ctx, fs, stat)
		})

	return err
}

func (w *wrappedHandler) ToHandle(ctx context.Context, fs billy.Filesystem, path []string) []byte {
	if wrapped, ok := fs.(*wrappedFS); ok {
		fs = wrapped.inner
	}

	var result []byte
	_, _ = w.interceptors.Exec(ctx, "Handler.ToHandle", []any{path},
		func(ctx context.Context) ([]any, error) {
			result = w.inner.ToHandle(ctx, fs, path)

			return []any{result}, nil
		})

	return result
}

func (w *wrappedHandler) FromHandle(ctx context.Context, fh []byte) (billy.Filesystem, []string, error) {
	var fs billy.Filesystem
	var paths []string

	_, err := w.interceptors.Exec(ctx, "Handler.FromHandle", nil,
		func(ctx context.Context) ([]any, error) {
			var err error
			fs, paths, err = w.inner.FromHandle(ctx, fh)

			return []any{fs, paths}, err
		})

	return WrapFilesystem(ctx, fs, w.interceptors), paths, err
}

func (w *wrappedHandler) InvalidateHandle(ctx context.Context, fs billy.Filesystem, fh []byte) error {
	if wrapped, ok := fs.(*wrappedFS); ok {
		fs = wrapped.inner
	}

	_, err := w.interceptors.Exec(ctx, "Handler.InvalidateHandle", nil,
		func(ctx context.Context) ([]any, error) {
			return nil, w.inner.InvalidateHandle(ctx, fs, fh)
		})

	return err
}

func (w *wrappedHandler) HandleLimit() int {
	return w.inner.HandleLimit()
}
