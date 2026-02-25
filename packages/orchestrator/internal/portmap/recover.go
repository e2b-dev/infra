package portmap

import (
	"context"
	"fmt"

	"github.com/zeldovich/go-rpcgen/rfc1057"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type recovery struct {
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
	inner rfc1057.PMAP_PROG_PMAP_VERS_handler
}

var _ rfc1057.PMAP_PROG_PMAP_VERS_handler = (*recovery)(nil)

func wrapWithRecovery(ctx context.Context, h rfc1057.PMAP_PROG_PMAP_VERS_handler) *recovery {
	return &recovery{ctx: ctx, inner: h}
}

func (h *recovery) PMAPPROC_NULL() {
	defer h.tryRecovery("PMAPPROC_NULL")

	h.inner.PMAPPROC_NULL()
}

func (h *recovery) PMAPPROC_SET(mapping rfc1057.Mapping) rfc1057.Xbool {
	defer h.tryRecovery("PMAPPROC_SET")

	return h.inner.PMAPPROC_SET(mapping)
}

func (h *recovery) PMAPPROC_UNSET(mapping rfc1057.Mapping) rfc1057.Xbool {
	defer h.tryRecovery("PMAPPROC_UNSET")

	return h.inner.PMAPPROC_UNSET(mapping)
}

func (h *recovery) PMAPPROC_GETPORT(mapping rfc1057.Mapping) rfc1057.Uint32 {
	defer h.tryRecovery("PMAPPROC_GETPORT")

	return h.inner.PMAPPROC_GETPORT(mapping)
}

func (h *recovery) PMAPPROC_DUMP() rfc1057.Pmaplist {
	defer h.tryRecovery("PMAPPROC_DUMP")

	return h.inner.PMAPPROC_DUMP()
}

func (h *recovery) PMAPPROC_CALLIT(args rfc1057.Call_args) rfc1057.Call_result {
	defer h.tryRecovery("PMAPPROC_CALLIT")

	return h.inner.PMAPPROC_CALLIT(args)
}

func (h *recovery) tryRecovery(name string) {
	if r := recover(); r != nil { //nolint:revive
		logger.L().Error(h.ctx, fmt.Sprintf("panic in %q portmap handler", name), zap.Any("panic", r))
	}
}
