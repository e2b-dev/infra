package chrooted

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/filesystem"
)

func (fs *Chrooted) Create(filename string) (f *os.File, err error) {
	err = fs.act(func() error {
		f, err = os.Create(filename)

		return err
	})

	return f, err
}

func (fs *Chrooted) Open(filename string) (f *os.File, err error) {
	err = fs.act(func() error {
		f, err = os.Open(filename)

		return err
	})

	return
}

func (fs *Chrooted) OpenFile(filename string, flag int, perm os.FileMode) (f *os.File, err error) {
	err = fs.act(func() error {
		f, err = os.OpenFile(filename, flag, perm)

		return err
	})

	return
}

func (fs *Chrooted) EvalSymlinks(filename string) (p string, e error) {
	e = fs.act(func() error {
		p, e = filepath.EvalSymlinks(filename)

		return e
	})

	return
}

func (fs *Chrooted) Stat(filename string) (info os.FileInfo, err error) {
	err = fs.act(func() error {
		info, err = os.Stat(filename)

		return err
	})

	return
}

func (fs *Chrooted) GetEntry(filename string) (info filesystem.EntryInfo, err error) {
	err = fs.act(func() error {
		info, err = filesystem.GetEntryFromPath(filename)

		return err
	})

	return
}

func (fs *Chrooted) Rename(oldpath, newpath string) (err error) {
	err = fs.act(func() error {
		return os.Rename(oldpath, newpath)
	})

	return
}

func (fs *Chrooted) Remove(filename string) (err error) {
	err = fs.act(func() error {
		return os.Remove(filename)
	})

	return
}

func (fs *Chrooted) RemoveAll(filename string) (err error) {
	err = fs.act(func() error {
		return os.RemoveAll(filename)
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

func (fs *Chrooted) TempFile(dir, prefix string) (f *os.File, err error) {
	err = fs.act(func() error {
		f, err = os.CreateTemp(dir, prefix)

		return err
	})

	return
}

func (fs *Chrooted) ReadDir(path string) (info []os.FileInfo, err error) {
	err = fs.act(func() error {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}

		info = make([]os.FileInfo, 0, len(entries))
		for _, entry := range entries {
			fi, err := entry.Info()
			if err != nil {
				return err
			}

			info = append(info, fi)
		}

		return nil
	})

	return
}

func (fs *Chrooted) Mkdir(filename string, perm os.FileMode) (err error) {
	err = fs.act(func() error {
		return os.Mkdir(filename, perm)
	})

	return
}

func (fs *Chrooted) MkdirAll(filename string, perm os.FileMode) (err error) {
	err = fs.act(func() error {
		return os.MkdirAll(filename, perm)
	})

	return
}

func (fs *Chrooted) Lstat(filename string) (info os.FileInfo, err error) {
	err = fs.act(func() error {
		info, err = os.Lstat(filename)

		return err
	})

	return
}

func (fs *Chrooted) Symlink(target, link string) (err error) {
	err = fs.act(func() error {
		return os.Symlink(target, link)
	})

	return
}

func (fs *Chrooted) Readlink(link string) (target string, err error) {
	err = fs.act(func() error {
		target, err = os.Readlink(link)

		return err
	})

	return
}

func (fs *Chrooted) Chroot(_ string) (*Chrooted, error) {
	return nil, fmt.Errorf("chroot not supported")
}

func (fs *Chrooted) Root() string {
	return fs.ActualRoot
}
