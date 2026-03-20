package chrooted

import (
	"os"
	"time"
)

func (fs *Chrooted) Chmod(name string, mode os.FileMode) error {
	return fs.act(func() error {
		return os.Chmod(name, mode)
	})
}

func (fs *Chrooted) Lchown(name string, uid, gid int) error {
	return fs.act(func() error {
		return os.Lchown(name, uid, gid)
	})
}

func (fs *Chrooted) Chown(name string, uid, gid int) error {
	return fs.act(func() error {
		return os.Chown(name, uid, gid)
	})
}

func (fs *Chrooted) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return fs.act(func() error {
		return os.Chtimes(name, atime, mtime)
	})
}
