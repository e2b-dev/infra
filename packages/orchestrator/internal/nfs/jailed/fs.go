package jailed

import (
	"os"
	"strings"

	"github.com/go-git/go-billy/v5"
)

type jailedFS struct {
	prefix string
	inner  billy.Filesystem
}

var _ billy.Filesystem = (*jailedFS)(nil)

func wrapFS(fs billy.Filesystem, prefix string) billy.Filesystem {
	return jailedFS{
		prefix: prefix,
		inner:  fs,
	}
}

func (j jailedFS) Unwrap() billy.Filesystem {
	return j.inner
}

func (j jailedFS) Create(filename string) (billy.File, error) {
	return j.inner.Create(filename)
}

func (j jailedFS) Open(filename string) (billy.File, error) {
	return j.inner.Open(filename)
}

func (j jailedFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	return j.inner.OpenFile(filename, flag, perm)
}

func (j jailedFS) Stat(filename string) (os.FileInfo, error) {
	return j.inner.Stat(filename)
}

func (j jailedFS) Rename(oldpath, newpath string) error {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) Remove(filename string) error {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) Join(elem ...string) string {
	if j.needsPrefix(elem) {
		elem = append([]string{j.prefix}, elem...)
	}

	return j.inner.Join(elem...)
}

func (j jailedFS) TempFile(dir, prefix string) (billy.File, error) {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) ReadDir(path string) ([]os.FileInfo, error) {
	items, err := j.inner.ReadDir(path)
	if err != nil {
		return nil, err
	}

	prefix := j.prefix + "/"
	for index, item := range items {
		items[index] = hidePrefix(item, prefix)
	}

	return items, nil
}

func (j jailedFS) MkdirAll(filename string, perm os.FileMode) error {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) Lstat(filename string) (os.FileInfo, error) {
	return j.inner.Lstat(filename)
}

func (j jailedFS) Symlink(target, link string) error {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) Readlink(link string) (string, error) {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) Chroot(path string) (billy.Filesystem, error) {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) Root() string {
	// TODO implement me
	panic("implement me")
}

func (j jailedFS) needsPrefix(elem []string) bool {
	if len(elem) == 0 {
		return true
	}

	if elem[0] == j.prefix {
		return false
	}

	if strings.HasPrefix(elem[0], j.prefix+"/") {
		return false
	}

	return true
}
