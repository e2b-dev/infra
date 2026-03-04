package jailed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
)

type jailedFS struct {
	prefix string
	inner  billy.Filesystem
}

var _ billy.Filesystem = (*jailedFS)(nil)

// Join expands the request into the chrooted filesystem. It is called by willscott/go-nfs
// on every request, and is responsible for making sure that the request is not escaping into the rest of the OS.
func (j jailedFS) Join(elem ...string) string {
	// Start with inner's join and normalize separators and dots.
	p := j.inner.Join(elem...)
	p = filepath.Clean(p)

	// If the cleaned path is already inside the jail prefix, keep it as-is to
	// avoid duplicating the prefix (e.g., Join("/jail", "a")).
	if p == j.prefix || strings.HasPrefix(p, j.prefix+"/") {
		return p
	}

	// Force the path to stay within the jail by cleaning it as an absolute path,
	// then stripping the leading slash before joining with the prefix.
	s := filepath.Clean("/" + p)

	return filepath.Join(j.prefix, strings.TrimPrefix(s, "/"))
}

func (j jailedFS) Unwrap() billy.Filesystem {
	return j.inner
}

func (j jailedFS) Create(filename string) (billy.File, error) {
	f, err := j.inner.Create(filename)

	return tryWrapBillyFile(f, j.prefix), err
}

func (j jailedFS) Open(filename string) (billy.File, error) {
	f, err := j.inner.Open(filename)

	return tryWrapBillyFile(f, j.prefix), err
}

func (j jailedFS) String() string {
	return fmt.Sprintf("jailedFS{prefix=%s, inner=%v}", j.prefix, j.inner)
}

func (j jailedFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	file, err := j.inner.OpenFile(filename, flag, perm)

	return tryWrapBillyFile(file, j.prefix), err
}

func (j jailedFS) Stat(filename string) (os.FileInfo, error) {
	file, err := j.inner.Stat(filename)

	return tryWrapOSFile(file, j.prefix), err
}

func (j jailedFS) Rename(oldpath, newpath string) error {
	return j.inner.Rename(oldpath, newpath)
}

func (j jailedFS) Remove(filename string) error {
	return j.inner.Remove(filename)
}

func (j jailedFS) TempFile(dir, prefix string) (billy.File, error) {
	f, err := j.inner.TempFile(dir, prefix)

	return tryWrapBillyFile(f, j.prefix), err
}

func (j jailedFS) ReadDir(path string) ([]os.FileInfo, error) {
	items, err := j.inner.ReadDir(path)
	if err != nil {
		return nil, err
	}

	prefix := j.prefix + "/"
	for index, item := range items {
		items[index] = tryWrapOSFile(item, prefix)
	}

	return items, nil
}

func (j jailedFS) MkdirAll(filename string, perm os.FileMode) error {
	return j.inner.MkdirAll(filename, perm)
}

func (j jailedFS) Lstat(filename string) (os.FileInfo, error) {
	f, err := j.inner.Lstat(filename)

	return tryWrapOSFile(f, j.prefix), err
}

func (j jailedFS) Symlink(target, link string) error {
	return j.inner.Symlink(target, link)
}

func (j jailedFS) Readlink(link string) (string, error) {
	return j.inner.Readlink(link)
}

func (j jailedFS) Chroot(path string) (billy.Filesystem, error) {
	fs, err := j.inner.Chroot(path)

	return tryWrapFS(fs, j.prefix), err
}

func (j jailedFS) Root() string {
	return j.inner.Root()
}

func tryWrapFS(fs billy.Filesystem, prefix string) billy.Filesystem {
	if fs == nil {
		return nil
	}

	return jailedFS{
		prefix: prefix,
		inner:  fs,
	}
}
