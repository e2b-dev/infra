package portmap

import (
	"context"
	"sync"

	portmap "github.com/zeldovich/go-rpcgen/rfc1057"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type handlers struct {
	ctx  context.Context //nolint:containedctx // can't change the API, still need it
	maps map[key]portmap.Uint32
	lock sync.RWMutex
}

func newHandlers() *handlers {
	return &handlers{maps: make(map[key]portmap.Uint32)}
}

var _ portmap.PMAP_PROG_PMAP_VERS_handler = (*handlers)(nil)

func (h *handlers) PMAPPROC_NULL() {}

func (h *handlers) PMAPPROC_SET(mapping portmap.Mapping) portmap.Xbool {
	h.lock.Lock()
	defer h.lock.Unlock()

	h.maps[key{
		Prog: mapping.Prog,
		Vers: mapping.Vers,
		Prot: mapping.Prot,
	}] = portmap.Uint32(mapping.Port)

	return true
}

func (h *handlers) PMAPPROC_UNSET(_ portmap.Mapping) portmap.Xbool {
	return false
}

func (h *handlers) getPortByKey(k key) (portmap.Uint32, bool) {
	h.lock.RLock()
	defer h.lock.RUnlock()

	port, ok := h.maps[k]

	return port, ok
}

func (h *handlers) PMAPPROC_GETPORT(mapping portmap.Mapping) portmap.Uint32 {
	logger.L().Debug(h.ctx, "[portmap handler] searching for a map",
		zap.Int("len", len(h.maps)),
		zap.Uint32("prog", mapping.Prog),
		zap.Uint32("vers", mapping.Vers),
		zap.Uint32("prot", mapping.Prot))

	port, ok := h.getPortByKey(key{
		Prog: mapping.Prog,
		Vers: mapping.Vers,
		Prot: mapping.Prot,
	})
	if !ok {
		logger.L().Warn(h.ctx, "[portmap handler] port not found")

		return 0
	}

	logger.L().Debug(h.ctx, "[portmap handler] port found",
		zap.Int("len", len(h.maps)),
		zap.Uint32("prog", mapping.Prog),
		zap.Uint32("vers", mapping.Vers),
		zap.Uint32("prot", mapping.Prot),
		zap.Uint32("port", uint32(port)))

	return port
}

func (h *handlers) PMAPPROC_DUMP() portmap.Pmaplist {
	h.lock.RLock()
	defer h.lock.RUnlock()

	var head portmap.Pmaplist

	current := &head
	for key, val := range h.maps {
		item := portmap.Mapping{
			Prog: key.Prog,
			Vers: key.Vers,
			Prot: key.Prot,
			Port: uint32(val),
		}

		current.P = &portmap.Pmaplistelem{
			Map: item,
		}
		current = &current.P.Next
	}

	return head
}

func (h *handlers) PMAPPROC_CALLIT(args portmap.Call_args) portmap.Call_result {
	logger.L().Debug(h.ctx, "[portmap handler] calling the service",
		zap.Uint32("prog", args.Prog),
		zap.Uint32("vers", args.Vers),
		zap.Uint32("proc", args.Proc))

	return portmap.Call_result{}
}
