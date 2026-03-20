package chroot

import (
	"os"
	"time"

	"github.com/go-git/go-billy/v5"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/chrooted"
)

type wrappedChange struct {
	c *chrooted.Chrooted
}

func (w wrappedChange) Chmod(name string, mode os.FileMode) error {
	return w.c.Chmod(name, mode)
}

func (w wrappedChange) Lchown(name string, uid, gid int) error {
	return w.c.Lchown(name, uid, gid)
}

func (w wrappedChange) Chown(name string, uid, gid int) error {
	return w.c.Chown(name, uid, gid)
}

func (w wrappedChange) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return w.c.Chtimes(name, atime, mtime)
}

var _ billy.Change = (*wrappedChange)(nil)

func wrapChange(c *chrooted.Chrooted) *wrappedChange {
	return &wrappedChange{c: c}
}
