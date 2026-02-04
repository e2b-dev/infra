package portmap

import (
	"context"

	"github.com/zeldovich/go-rpcgen/rfc1057"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type loggable struct {
	ctx   context.Context //nolint:containedctx // can't change the API, still need it
	inner rfc1057.PMAP_PROG_PMAP_VERS_handler
}

var _ rfc1057.PMAP_PROG_PMAP_VERS_handler = (*loggable)(nil)

func wrapWithLogging(ctx context.Context, h rfc1057.PMAP_PROG_PMAP_VERS_handler) *loggable {
	return &loggable{ctx: ctx, inner: h}
}

func (l *loggable) PMAPPROC_NULL() {
	logger.L().Info(l.ctx, "[portmap] PMAPPROC_NULL: begin")
	defer func() { logger.L().Info(l.ctx, "[portmap] PMAPPROC_NULL: end") }()

	l.inner.PMAPPROC_NULL()
}

func (l *loggable) PMAPPROC_SET(mapping rfc1057.Mapping) (b rfc1057.Xbool) {
	logger.L().Info(l.ctx, "[portmap] PMAPPROC_SET: begin", zap.Any("mapping", mapping))
	defer func() { logger.L().Info(l.ctx, "[portmap] PMAPPROC_SET: end", zap.Any("result", b)) }()

	return l.inner.PMAPPROC_SET(mapping)
}

func (l *loggable) PMAPPROC_UNSET(mapping rfc1057.Mapping) (b rfc1057.Xbool) {
	logger.L().Info(l.ctx, "[portmap] PMAPPROC_UNSET: begin", zap.Any("mapping", mapping))
	defer func() { logger.L().Info(l.ctx, "[portmap] PMAPPROC_UNSET: end", zap.Any("result", b)) }()

	return l.inner.PMAPPROC_UNSET(mapping)
}

func (l *loggable) PMAPPROC_GETPORT(mapping rfc1057.Mapping) (n rfc1057.Uint32) {
	logger.L().Info(l.ctx, "[portmap] PMAPPROC_GETPORT: begin", zap.Any("mapping", mapping))
	defer func() { logger.L().Info(l.ctx, "[portmap] PMAPPROC_GETPORT: end", zap.Any("result", n)) }()

	return l.inner.PMAPPROC_GETPORT(mapping)
}

func (l *loggable) PMAPPROC_DUMP() (p rfc1057.Pmaplist) {
	logger.L().Info(l.ctx, "[portmap] PMAPPROC_DUMP: begin")
	defer func() { logger.L().Info(l.ctx, "[portmap] PMAPPROC_DUMP: end", zap.Any("result", p)) }()

	return l.inner.PMAPPROC_DUMP()
}

func (l *loggable) PMAPPROC_CALLIT(args rfc1057.Call_args) (r rfc1057.Call_result) {
	logger.L().Info(l.ctx, "[portmap] PMAPPROC_CALLIT: begin", zap.Any("args", args))
	defer func() { logger.L().Info(l.ctx, "[portmap] PMAPPROC_CALLIT: end", zap.Any("result", r)) }()

	return l.inner.PMAPPROC_CALLIT(args)
}
