package chroot

import (
	"os"

	"github.com/go-git/go-billy/v5"
	"golang.org/x/sys/unix"
)

type wrappedFile struct {
	file *os.File
}

var _ billy.File = (*wrappedFile)(nil)

func (w *wrappedFile) Name() string {
	return w.file.Name()
}

func (w *wrappedFile) Write(p []byte) (n int, err error) {
	return w.file.Write(p)
}

func (w *wrappedFile) Read(p []byte) (n int, err error) {
	return w.file.Read(p)
}

func (w *wrappedFile) ReadAt(p []byte, off int64) (n int, err error) {
	return w.file.ReadAt(p, off)
}

func (w *wrappedFile) Seek(offset int64, whence int) (int64, error) {
	return w.file.Seek(offset, whence)
}

func (w *wrappedFile) Close() error {
	return w.file.Close()
}

func (w *wrappedFile) Lock() error {
	return unix.Flock(int(w.file.Fd()), unix.LOCK_EX)
}

func (w *wrappedFile) Unlock() error {
	return unix.Flock(int(w.file.Fd()), unix.LOCK_UN)
}

func (w *wrappedFile) Truncate(size int64) error {
	return w.file.Truncate(size)
}

func maybeWrap(f *os.File) billy.File {
	if f == nil {
		return nil
	}

	return &wrappedFile{file: f}
}
