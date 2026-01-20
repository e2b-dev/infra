package portmap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/willscott/go-nfs-client/nfs"
	portmap "github.com/zeldovich/go-rpcgen/rfc1057"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type key struct {
	Prog uint32
	Vers uint32
	Prot uint32
}

type PortMap struct {
	h *handlers
	s *portmap.Server
}

func NewPortMap(ctx context.Context) *PortMap {
	s := portmap.MakeServer()
	h := newHandlers()

	var handler portmap.PMAP_PROG_PMAP_VERS_handler
	handler = h
	handler = newRecovery(ctx, handler)
	handler = newLoggedHandler(ctx, handler)

	regs := portmap.PMAP_PROG_PMAP_VERS_regs(handler)
	s.RegisterMany(regs)

	return &PortMap{
		h: h,
		s: s,
	}
}

func (pm *PortMap) RegisterPort(ctx context.Context, port uint32) {
	logger.L().Info(ctx, "registering port", zap.Uint32("port", port))

	pm.h.PMAPPROC_SET(portmap.Mapping{
		Prog: nfs.Nfs3Prog,
		Vers: nfs.Nfs3Vers,
		Prot: portmap.IPPROTO_TCP,
		Port: port,
	})

	pm.h.PMAPPROC_SET(portmap.Mapping{
		Prog: 100005, // mountd
		Vers: nfs.Nfs3Vers,
		Prot: portmap.IPPROTO_TCP,
		Port: port,
	})
}

func (pm *PortMap) Serve(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}

			return fmt.Errorf("error accepting connection: %w", err)
		}

		go pm.run(ctx, conn)
	}
}

func (pm *PortMap) run(ctx context.Context, conn net.Conn) {
	logger.L().Info(ctx, "[portmap] accepting connection",
		zap.String("local", conn.LocalAddr().String()),
		zap.String("remote", conn.RemoteAddr().String()),
	)

	err := pm.s.Run(conn)
	if ignoreEOF(err) != nil {
		logger.L().Warn(ctx, "portmap server error", zap.Error(err))

		return
	}
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}
