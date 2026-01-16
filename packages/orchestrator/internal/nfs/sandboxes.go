package nfs

import (
	"context"
	"log/slog"
	"net"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/willscott/go-nfs"
)

type sandboxJailsHandler struct {
	sandboxes *sandbox.Map
}

var _ nfs.Handler = (*sandboxJailsHandler)(nil)

func newSandboxJailsHandler(sandboxes *sandbox.Map) nfs.Handler {
	return &sandboxJailsHandler{
		sandboxes: sandboxes,
	}
}

func (s sandboxJailsHandler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	sbx, err := s.sandboxes.GetByHostPort(conn.RemoteAddr().String())
	if err != nil {
		slog.Warn("failed to get sandbox by host/port", "error", err)
		return nfs.MountStatusErrIO, nil, nil
	}

	fs := memfs.New()
	fp, err := fs.OpenFile("/sandbox-id.txt", os.O_WRONLY|os.O_CREATE, 0o666)
	if err != nil {
		slog.Warn("failed to open /sandbox-id.txt", "error", err)
		return nfs.MountStatusErrIO, nil, nil
	}
	defer logDefer("failed to close sandbox-id.txt", fp.Close)

	if _, err := fp.Write([]byte(sbx.Metadata.Runtime.SandboxID)); err != nil {
		slog.Warn("failed to write /sandbox-id.txt", "error", err)
		return nfs.MountStatusErrIO, nil, nil
	}

	return nfs.MountStatusOk, fs, nil
}

func logDefer(msg string, fn func() error) {
	if err := fn(); err != nil {
		slog.Warn(msg, "error", err)
	}
}

func (s sandboxJailsHandler) Change(filesystem billy.Filesystem) billy.Change {
	//TODO implement me
	panic("implement me: Change")
}

func (s sandboxJailsHandler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) error {
	//TODO implement me
	panic("implement me: FSStat")
}

func (s sandboxJailsHandler) ToHandle(fs billy.Filesystem, path []string) []byte {
	if fs == nil {
		return nil
	}

	//TODO implement me
	panic("implement me: ToHandle")
}

func (s sandboxJailsHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	//TODO implement me
	panic("implement me: FromHandle")
}

func (s sandboxJailsHandler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) error {
	//TODO implement me
	panic("implement me: InvalidateHandle")
}

func (s sandboxJailsHandler) HandleLimit() int {
	//TODO implement me
	panic("implement me: HandleLimit")
}
