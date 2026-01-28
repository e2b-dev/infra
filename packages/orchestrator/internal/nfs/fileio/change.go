package fileio

import (
	"fmt"
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

// change implements billy.Change using direct filesystem operations.
type change struct {
	fs billy.Filesystem
}

func (c change) String() string {
	return fmt.Sprintf("change{fs=%v}", c.fs)
}

var _ billy.Change = (*change)(nil)

func newChange(fs billy.Filesystem) *change {
	return &change{fs: fs}
}

func (c change) resolvePath(name string) string {
	if localFS, ok := c.fs.(*LocalFS); ok {
		return localFS.resolvePath(name)
	}

	return name
}

func (c change) Chmod(name string, mode os.FileMode) error {
	path := c.resolvePath(name)

	return os.Chmod(path, mode)
}

func (c change) Lchown(name string, uid, gid int) error {
	path := c.resolvePath(name)

	return os.Lchown(path, uid, gid)
}

func (c change) Chown(name string, uid, gid int) error {
	path := c.resolvePath(name)

	return os.Chown(path, uid, gid)
}

func (c change) Chtimes(name string, atime time.Time, mtime time.Time) error {
	path := c.resolvePath(name)

	return os.Chtimes(path, atime, mtime)
}
