package chrooted

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
)

var _ billy.Filesystem = (*Chrooted)(nil)

func (fs *Chrooted) Create(filename string) (f billy.File, e error) {
	e = fs.act(func(fs billy.Filesystem) error {
		f, e = fs.Create(filename)

		return e
	})

	return
}

func (fs *Chrooted) Open(filename string) (f billy.File, err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		f, err = fs.Open(filename)

		return err
	})

	return
}

func (fs *Chrooted) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		f, err = fs.OpenFile(filename, flag, perm)

		return err
	})

	return
}

func (fs *Chrooted) Stat(filename string) (info os.FileInfo, err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		info, err = fs.Stat(filename)

		return err
	})

	return
}

func (fs *Chrooted) Rename(oldpath, newpath string) (err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		return fs.Rename(oldpath, newpath)
	})

	return
}

func (fs *Chrooted) Remove(filename string) (err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		return fs.Remove(filename)
	})

	return
}

func (fs *Chrooted) Join(elem ...string) string {
	path := filepath.Join(elem...)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path
}

func (fs *Chrooted) TempFile(dir, prefix string) (f billy.File, err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		f, err = fs.TempFile(dir, prefix)

		return err
	})

	return
}

func (fs *Chrooted) ReadDir(path string) (info []os.FileInfo, err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		info, err = fs.ReadDir(path)

		return err
	})

	return
}

func (fs *Chrooted) MkdirAll(filename string, perm os.FileMode) (err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		return fs.MkdirAll(filename, perm)
	})

	return
}

func (fs *Chrooted) Lstat(filename string) (info os.FileInfo, err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		info, err = fs.Lstat(filename)

		return err
	})

	return
}

func (fs *Chrooted) Symlink(target, link string) (err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		return fs.Symlink(target, link)
	})

	return
}

func (fs *Chrooted) Readlink(link string) (target string, err error) {
	err = fs.act(func(fs billy.Filesystem) error {
		target, err = fs.Readlink(link)

		return err
	})

	return
}

func (fs *Chrooted) Chroot(_ string) (billy.Filesystem, error) {
	return nil, fmt.Errorf("chroot not supported")
}

func (fs *Chrooted) Root() string {
	return "/"
}
