package recovery

import (
	"os"

	"github.com/go-git/go-billy/v5"
)

type filesystem struct {
	inner billy.Filesystem
}

var _ billy.Filesystem = (*filesystem)(nil)

func wrapFS(fs billy.Filesystem) billy.Filesystem {
	if fs == nil {
		return nil
	}

	return &filesystem{inner: fs}
}

func (fs *filesystem) Create(filename string) (billy.File, error) {
	defer tryRecovery("Create")
	file, err := fs.inner.Create(filename)

	return wrapFile(file), err
}

func (fs *filesystem) Open(filename string) (billy.File, error) {
	defer tryRecovery("Open")
	file, err := fs.inner.Open(filename)

	return wrapFile(file), err
}

func (fs *filesystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	defer tryRecovery("OpenFile")
	file, err := fs.inner.OpenFile(filename, flag, perm)

	return wrapFile(file), err
}

func (fs *filesystem) Stat(filename string) (os.FileInfo, error) {
	defer tryRecovery("Stat")

	return fs.inner.Stat(filename)
}

func (fs *filesystem) Rename(oldpath, newpath string) error {
	defer tryRecovery("Rename")

	return fs.inner.Rename(oldpath, newpath)
}

func (fs *filesystem) Remove(filename string) error {
	defer tryRecovery("Remove")

	return fs.inner.Remove(filename)
}

func (fs *filesystem) Join(elem ...string) string {
	defer tryRecovery("Join")

	return fs.inner.Join(elem...)
}

func (fs *filesystem) TempFile(dir, prefix string) (billy.File, error) {
	defer tryRecovery("TempFile")
	file, err := fs.inner.TempFile(dir, prefix)

	return wrapFile(file), err
}

func (fs *filesystem) ReadDir(path string) ([]os.FileInfo, error) {
	defer tryRecovery("ReadDir")

	return fs.inner.ReadDir(path)
}

func (fs *filesystem) MkdirAll(filename string, perm os.FileMode) error {
	defer tryRecovery("MkdirAll")

	return fs.inner.MkdirAll(filename, perm)
}

func (fs *filesystem) Lstat(filename string) (os.FileInfo, error) {
	defer tryRecovery("Lstat")

	return fs.inner.Lstat(filename)
}

func (fs *filesystem) Symlink(target, link string) error {
	defer tryRecovery("Symlink")

	return fs.inner.Symlink(target, link)
}

func (fs *filesystem) Readlink(link string) (string, error) {
	defer tryRecovery("Readlink")

	return fs.inner.Readlink(link)
}

func (fs *filesystem) Chroot(path string) (billy.Filesystem, error) {
	defer tryRecovery("Chroot")
	inner, err := fs.inner.Chroot(path)
	if err != nil {
		return nil, err
	}

	return wrapFS(inner), nil
}

func (fs *filesystem) Root() string {
	defer tryRecovery("Root")

	return fs.inner.Root()
}
