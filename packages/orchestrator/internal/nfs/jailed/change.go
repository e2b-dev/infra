package jailed

import (
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type jailedChange struct {
	inner billy.Change
}

var _ billy.Change = (*jailedChange)(nil)

func wrapChange(change billy.Change) billy.Change {
	return jailedChange{change}
}

func (c jailedChange) Chmod(name string, mode os.FileMode) error {
	return c.inner.Chmod(name, mode)
}

func (c jailedChange) Lchown(name string, uid, gid int) error {
	return c.inner.Lchown(name, uid, gid)
}

func (c jailedChange) Chown(name string, uid, gid int) error {
	return c.Chown(name, uid, gid)
}

func (c jailedChange) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return c.inner.Chtimes(name, atime, mtime)
}
