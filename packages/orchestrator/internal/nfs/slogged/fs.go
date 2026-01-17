package slogged

import (
	"os"

	"github.com/go-git/go-billy/v5"
)

type loggedFS struct {
	inner billy.Filesystem
}

var _ billy.Filesystem = (*loggedFS)(nil)

func newFS(fs billy.Filesystem) loggedFS {
	return loggedFS{fs}
}

func (l loggedFS) Unwrap() billy.Filesystem {
	return l.inner
}

func (l loggedFS) Create(filename string) (f billy.File, err error) {
	slogStart("FS.Create", filename)
	defer func() { slogEndWithError("FS.Create", err) }()

	f, err = l.inner.Create(filename)
	f = wrapFile(f)

	return
}

func (l loggedFS) Open(filename string) (f billy.File, err error) {
	slogStart("FS.Open", filename)
	defer func() { slogEndWithError("FS.Open", err) }()

	f, err = l.inner.Open(filename)
	f = wrapFile(f)

	return
}

func (l loggedFS) OpenFile(filename string, flag int, perm os.FileMode) (f billy.File, err error) {
	slogStart("FS.OpenFile", filename, flag, perm)
	defer func() { slogEndWithError("FS.OpenFile", err) }()

	f, err = l.inner.OpenFile(filename, flag, perm)
	f = wrapFile(f)

	return
}

func (l loggedFS) Stat(filename string) (fi os.FileInfo, err error) {
	slogStart("FS.Stat", filename)
	defer func() { slogEndWithError("FS.Stat", err) }()

	return l.inner.Stat(filename)
}

func (l loggedFS) Rename(oldpath, newpath string) (err error) {
	slogStart("FS.Rename")
	defer func() { slogEndWithError("FS.Rename", err) }()

	return l.inner.Rename(oldpath, newpath)
}

func (l loggedFS) Remove(filename string) (err error) {
	slogStart("FS.Remove")
	defer func() { slogEndWithError("FS.Remove", err) }()

	return l.inner.Remove(filename)
}

func (l loggedFS) Join(elem ...string) (path string) {
	slogStart("FS.Join", elem)
	defer func() { slogEnd("FS.Join", path) }()

	return l.inner.Join(elem...)
}

func (l loggedFS) TempFile(dir, prefix string) (f billy.File, err error) {
	slogStart("FS.TempFile")
	defer func() { slogEndWithError("FS.TempFile", err) }()

	f, err = l.inner.TempFile(dir, prefix)
	f = wrapFile(f)

	return
}

func (l loggedFS) ReadDir(path string) (fi []os.FileInfo, err error) {
	slogStart("FS.ReadDir", path)
	defer func() { slogEndWithError("FS.ReadDir", err) }()

	return l.inner.ReadDir(path)
}

func (l loggedFS) MkdirAll(filename string, perm os.FileMode) (err error) {
	slogStart("FS.MkdirAll")
	defer func() { slogEndWithError("FS.MkdirAll", err) }()

	return l.inner.MkdirAll(filename, perm)
}

func (l loggedFS) Lstat(filename string) (fi os.FileInfo, err error) {
	slogStart("FS.Lstat", filename)
	defer func() { slogEndWithError("FS.Lstat", err) }()

	return l.inner.Lstat(filename)
}

func (l loggedFS) Symlink(target, link string) (err error) {
	slogStart("FS.Symlink")
	defer func() { slogEndWithError("FS.Symlink", err) }()

	return l.inner.Symlink(target, link)
}

func (l loggedFS) Readlink(link string) (target string, err error) {
	slogStart("FS.Readlink")
	defer func() { slogEndWithError("FS.Readlink", err) }()

	return l.inner.Readlink(link)
}

func (l loggedFS) Chroot(path string) (fs billy.Filesystem, err error) {
	slogStart("FS.Chroot")
	defer func() { slogEndWithError("FS.Chroot", err) }()

	inner, err := l.inner.Chroot(path)
	if err != nil {
		return nil, err
	}

	return newFS(inner), nil
}

func (l loggedFS) Root() string {
	return l.inner.Root()
}
