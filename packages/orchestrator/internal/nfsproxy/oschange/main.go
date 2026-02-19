package oschange

import (
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-billy/v5"
)

type Change struct{ base string }

func NewChange(base string) Change {
	return Change{base: base}
}

func (p Change) Chmod(name string, mode os.FileMode) error {
	return os.Chmod(filepath.Join(p.base, name), mode)
}

func (p Change) Lchown(name string, uid, gid int) error {
	return os.Lchown(filepath.Join(p.base, name), uid, gid)
}

func (p Change) Chown(name string, uid, gid int) error {
	return os.Chown(filepath.Join(p.base, name), uid, gid)
}

func (p Change) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return os.Chtimes(filepath.Join(p.base, name), atime, mtime)
}

var _ billy.Change = (*Change)(nil)
