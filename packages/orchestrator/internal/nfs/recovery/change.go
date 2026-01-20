package recovery

import (
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type change struct {
	inner billy.Change
}

var _ billy.Change = (*change)(nil)

func wrapChange(c billy.Change) billy.Change {
	if c == nil {
		return nil
	}

	return &change{inner: c}
}

func (c *change) Chmod(name string, mode os.FileMode) error {
	defer tryRecovery("Chmod")

	return c.inner.Chmod(name, mode)
}

func (c *change) Lchown(name string, uid, gid int) error {
	defer tryRecovery("Lchown")

	return c.inner.Lchown(name, uid, gid)
}

func (c *change) Chown(name string, uid, gid int) error {
	defer tryRecovery("Chown")

	return c.inner.Chown(name, uid, gid)
}

func (c *change) Chtimes(name string, atime time.Time, mtime time.Time) error {
	defer tryRecovery("Chtimes")

	return c.inner.Chtimes(name, atime, mtime)
}
