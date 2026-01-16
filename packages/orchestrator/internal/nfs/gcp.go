package nfs

import (
	"context"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type gcpHandler struct{}

var _ nfs.Handler = (*gcpHandler)(nil)

func (g gcpHandler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	//TODO implement me
	panic("implement me")
}

func (g gcpHandler) Change(filesystem billy.Filesystem) billy.Change {
	//TODO implement me
	panic("implement me")
}

func (g gcpHandler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) error {
	//TODO implement me
	panic("implement me")
}

func (g gcpHandler) ToHandle(fs billy.Filesystem, path []string) []byte {
	//TODO implement me
	panic("implement me")
}

func (g gcpHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	//TODO implement me
	panic("implement me")
}

func (g gcpHandler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) error {
	//TODO implement me
	panic("implement me")
}

func (g gcpHandler) HandleLimit() int {
	//TODO implement me
	panic("implement me")
}
