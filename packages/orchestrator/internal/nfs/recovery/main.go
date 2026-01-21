package recovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type Handler struct {
	inner nfs.Handler
}

var _ nfs.Handler = (*Handler)(nil)

func NewHandler(h nfs.Handler) *Handler {
	return &Handler{inner: h}
}

func (h Handler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	defer h.tryRecovery("Mount")
	s, fs, auth := h.inner.Mount(ctx, conn, request)
	fs = wrapFS(fs)

	return s, fs, auth
}

func (h Handler) Change(filesystem billy.Filesystem) billy.Change {
	defer h.tryRecovery("Change")
	c := h.inner.Change(filesystem)

	return wrapChange(c)
}

func (h Handler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) error {
	defer h.tryRecovery("FSStat")

	return h.inner.FSStat(ctx, filesystem, stat)
}

func (h Handler) ToHandle(fs billy.Filesystem, path []string) []byte {
	defer h.tryRecovery("ToHandle")

	return h.inner.ToHandle(fs, path)
}

func (h Handler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	defer h.tryRecovery("FromHandle")

	return h.inner.FromHandle(fh)
}

func (h Handler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) error {
	defer h.tryRecovery("InvalidateHandle")

	return h.inner.InvalidateHandle(filesystem, bytes)
}

func (h Handler) HandleLimit() int {
	defer h.tryRecovery("HandleLimit")

	return h.inner.HandleLimit()
}

func (h Handler) tryRecovery(name string) {
	tryRecovery(name)
}

func tryRecovery(name string) {
	if r := recover(); r != nil {
		slog.Error(fmt.Sprintf("panic in %q nfs handler", name), "panic", r)
	}
}
