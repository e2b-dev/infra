package fileio

import (
	"context"
	"fmt"
	"net"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

// NFSHandler implements nfs.Handler for a local filesystem rooted at a given path.
type NFSHandler struct {
	rootPath string
}

func (h NFSHandler) String() string {
	return fmt.Sprintf("NFSHandler{rootPath=%s}", h.rootPath)
}

var _ nfs.Handler = (*NFSHandler)(nil)

// NewNFSHandler creates a new NFS handler backed by a local filesystem at rootPath.
func NewNFSHandler(rootPath string) *NFSHandler {
	return &NFSHandler{
		rootPath: rootPath,
	}
}

func (h NFSHandler) Mount(_ context.Context, _ net.Conn, req nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	fs := NewLocalFS(h.rootPath)

	subDir := filepath.Clean(string(req.Dirpath))
	if subDir != "" && subDir != "." && subDir != "/" {
		fullPath := filepath.Join(h.rootPath, subDir)
		if err := fs.MkdirAll(fullPath, 0o755); err != nil {
			return nfs.MountStatusErrIO, nil, nil
		}
	}

	return nfs.MountStatusOk, fs, nil
}

func (h NFSHandler) Change(filesystem billy.Filesystem) billy.Change {
	return newChange(filesystem)
}

func (h NFSHandler) FSStat(_ context.Context, _ billy.Filesystem, _ *nfs.FSStat) error {
	// TODO: implement using syscall.Statfs
	panic("implement me")
}

func (h NFSHandler) ToHandle(_ billy.Filesystem, _ []string) []byte {
	// TODO: implement
	panic("implement me")
}

func (h NFSHandler) FromHandle(_ []byte) (billy.Filesystem, []string, error) {
	// TODO: implement
	panic("implement me")
}

func (h NFSHandler) InvalidateHandle(_ billy.Filesystem, _ []byte) error {
	panic("implement me")
}

func (h NFSHandler) HandleLimit() int {
	// TODO: implement
	panic("implement me")
}
