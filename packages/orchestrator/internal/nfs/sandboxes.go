package nfs

import (
	"context"
	"log/slog"
	"net"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type sandboxJailsHandler struct {
	client    *storage.Client
	sandboxes *sandbox.Map

	gcsBucketName string
}

var _ nfs.Handler = (*sandboxJailsHandler)(nil)

func newSandboxJailsHandler(sandboxes *sandbox.Map, client *storage.Client, gcsBucketName string) nfs.Handler {
	return &sandboxJailsHandler{
		client:        client,
		sandboxes:     sandboxes,
		gcsBucketName: gcsBucketName,
	}
}

func (s sandboxJailsHandler) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	sbx, err := s.sandboxes.GetByHostPort(conn.RemoteAddr().String())
	if err != nil {
		slog.Warn("failed to get sandbox by host/port", "error", err)
		return nfs.MountStatusErrIO, nil, nil
	}

	fs := newPrefixedGCSBucket(ctx, s.client, s.gcsBucketName, sbx.Metadata.Runtime.SandboxID)

	return nfs.MountStatusOk, fs, nil
}

func logDefer(msg string, fn func() error) {
	if err := fn(); err != nil {
		slog.Warn(msg, "error", err)
	}
}

func (s sandboxJailsHandler) Change(filesystem billy.Filesystem) billy.Change {
	return noopChange{}
}

type noopChange struct {
}

func (n noopChange) Chmod(name string, mode os.FileMode) error {
	return nil
}

func (n noopChange) Lchown(name string, uid, gid int) error {
	return nil
}

func (n noopChange) Chown(name string, uid, gid int) error {
	return nil
}

func (n noopChange) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return nil
}

var _ billy.Change = (*noopChange)(nil)

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
