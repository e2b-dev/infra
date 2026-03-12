package chroot

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type GetFilesystem func(ctx context.Context, conn net.Addr, request nfs.MountRequest) (*chrooted.Chrooted, error)

type NFSHandler struct {
	fn GetFilesystem
}

var _ nfs.Handler = (*NFSHandler)(nil)

func NewNFSHandler(fn GetFilesystem) *NFSHandler {
	return &NFSHandler{fn: fn}
}

func (h *NFSHandler) Mount(
	ctx context.Context,
	conn net.Conn,
	request nfs.MountRequest,
) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	fs, err := h.fn(ctx, conn.RemoteAddr(), request)
	if err != nil {
		logger.L().Warn(ctx, "failed to get path",
			zap.String("request", string(request.Dirpath)),
			zap.Error(err))

		return nfs.MountStatusErrAcces, mountFailedFS{}, nil
	}

	return nfs.MountStatusOk, wrapChrooted(fs), nil
}

func (h *NFSHandler) Change(filesystem billy.Filesystem) billy.Change {
	for {
		isolated, ok := filesystem.(*wrappedFS)
		if ok {
			return wrapChange(isolated.chroot)
		}

		unwrappable, ok := filesystem.(interface{ Unwrap() billy.Filesystem })
		if !ok {
			panic(fmt.Sprintf("no idea how to find an *Chrooted from this filesystem: %T", filesystem))
		}

		filesystem = unwrappable.Unwrap()
	}
}

func (h *NFSHandler) FSStat(_ context.Context, _ billy.Filesystem, _ *nfs.FSStat) error {
	return nil
}

func (h *NFSHandler) ToHandle(_ billy.Filesystem, _ []string) []byte {
	panic("this should be intercepted by the caching handler")
}

func (h *NFSHandler) FromHandle(_ []byte) (billy.Filesystem, []string, error) {
	panic("this should be intercepted by the caching handler")
}

func (h *NFSHandler) InvalidateHandle(_ billy.Filesystem, _ []byte) error {
	panic("this should be intercepted by the caching handler")
}

func (h *NFSHandler) HandleLimit() int {
	panic("this should be intercepted by the caching handler")
}
