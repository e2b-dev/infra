package chroot

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type GetPath func(conn net.Addr, request nfs.MountRequest) (string, error)

type NFSHandler struct {
	fn GetPath
}

var _ nfs.Handler = (*NFSHandler)(nil)

func NewNFSHandler(fn GetPath) nfs.Handler {
	return &NFSHandler{fn: fn}
}

func (h NFSHandler) Mount(
	ctx context.Context,
	conn net.Conn,
	request nfs.MountRequest,
) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	path, err := h.fn(conn.RemoteAddr(), request)
	if err != nil {
		logger.L().Warn(ctx, "failed to get path",
			zap.String("request", string(request.Dirpath)),
			zap.Error(err))

		return nfs.MountStatusErrAcces, mountFailedFS{}, nil
	}

	fs, err := IsolateFileSystem(path)
	if err != nil {
		logger.L().Error(ctx, "failed to chroot",
			zap.String("request", string(request.Dirpath)),
			zap.String("path", path),
			zap.Error(err))

		return nfs.MountStatusErrAcces, mountFailedFS{}, nil
	}

	return nfs.MountStatusOk, fs, nil
}

func (h NFSHandler) Change(filesystem billy.Filesystem) billy.Change {
	for {
		isolated, ok := filesystem.(*IsolatedFS)
		if ok {
			return isolated
		}

		unwrappable, ok := filesystem.(interface{ Unwrap() billy.Filesystem })
		if !ok {
			panic(fmt.Sprintf("no idea how to find an *IsolatedFS from this filesystem: %T", filesystem))
		}

		filesystem = unwrappable.Unwrap()
	}
}

func (h NFSHandler) FSStat(_ context.Context, _ billy.Filesystem, _ *nfs.FSStat) error {
	return nil
}

func (h NFSHandler) ToHandle(_ billy.Filesystem, _ []string) []byte {
	panic("this should be intercepted by the caching handler")
}

func (h NFSHandler) FromHandle(_ []byte) (billy.Filesystem, []string, error) {
	panic("this should be intercepted by the caching handler")
}

func (h NFSHandler) InvalidateHandle(_ billy.Filesystem, _ []byte) error {
	panic("this should be intercepted by the caching handler")
}

func (h NFSHandler) HandleLimit() int {
	panic("this should be intercepted by the caching handler")
}
