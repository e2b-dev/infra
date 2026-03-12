package chrooted

import (
	"os"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
)

var _ billy.Change = (*Chrooted)(nil)

func fullPath(fs billy.Filesystem, name string) string {
	path := fs.Join(fs.Root(), name)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path
}

func (fs *Chrooted) Chmod(name string, mode os.FileMode) error {
	return fs.act(func(fs billy.Filesystem) error {
		return os.Chmod(fullPath(fs, name), mode)
	})
}

func (fs *Chrooted) Lchown(name string, uid, gid int) error {
	return fs.act(func(fs billy.Filesystem) error {
		return os.Lchown(fullPath(fs, name), uid, gid)
	})
}

func (fs *Chrooted) Chown(name string, uid, gid int) error {
	return fs.act(func(fs billy.Filesystem) error {
		return os.Chown(fullPath(fs, name), uid, gid)
	})
}

func (fs *Chrooted) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return fs.act(func(fs billy.Filesystem) error {
		return os.Chtimes(fullPath(fs, name), atime, mtime)
	})
}
