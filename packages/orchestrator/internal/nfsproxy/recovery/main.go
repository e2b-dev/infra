package recovery

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Handler struct {
	inner nfs.Handler
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
}

var _ nfs.Handler = (*Handler)(nil)

func WrapWithRecovery(ctx context.Context, h nfs.Handler) *Handler {
	return &Handler{inner: h, ctx: ctx}
}

func (h Handler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	defer h.tryRecovery(ctx, "Mount")
	s, fs, auth := h.inner.Mount(ctx, conn, request)
	fs = wrapFS(ctx, fs)

	return s, fs, auth
}

func (h Handler) Change(filesystem billy.Filesystem) billy.Change {
	defer h.tryRecovery(h.ctx, "Change")
	c := h.inner.Change(filesystem)

	return wrapChange(h.ctx, c)
}

func (h Handler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) (e error) {
	defer deferErrRecovery(ctx, "Handler.FSStat", &e)

	return h.inner.FSStat(ctx, filesystem, stat)
}

func (h Handler) ToHandle(fs billy.Filesystem, path []string) []byte {
	defer h.tryRecovery(h.ctx, "ToHandle")

	return h.inner.ToHandle(fs, path)
}

func (h Handler) FromHandle(fh []byte) (fs billy.Filesystem, path []string, e error) {
	defer deferErrRecovery(h.ctx, "Handler.FromHandle", &e)

	return h.inner.FromHandle(fh)
}

func (h Handler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) (e error) {
	defer deferErrRecovery(h.ctx, "Handler.InvalidateHandle", &e)

	return h.inner.InvalidateHandle(filesystem, bytes)
}

func (h Handler) HandleLimit() int {
	defer h.tryRecovery(h.ctx, "HandleLimit")

	return h.inner.HandleLimit()
}

func (h Handler) tryRecovery(ctx context.Context, name string) {
	tryRecovery(ctx, name)
}

func tryRecovery(ctx context.Context, name string) {
	if r := recover(); r != nil { //nolint:revive // tryRecovery is always called via defer
		logger.L().Error(ctx, fmt.Sprintf("panic in %q nfs handler", name),
			zap.Any("panic", r),
			zap.Stack("stack"),
		)
	}
}

var ErrPanic = fmt.Errorf("panic")

func deferErrRecovery(ctx context.Context, name string, perr *error) {
	if r := recover(); r != nil { //nolint:revive // always called via defer
		logger.L().Error(ctx, fmt.Sprintf("panic in %q nfs handler", name),
			zap.Any("panic", r),
			zap.Stack("stack"),
		)
		if perr != nil {
			*perr = ErrPanic
		}
	}
}
