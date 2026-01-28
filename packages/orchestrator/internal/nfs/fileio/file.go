package fileio

import (
	"fmt"
	"os"
	"syscall"

	"github.com/go-git/go-billy/v5"
)

// localFile wraps os.File to implement billy.File.
type localFile struct {
	file *os.File
}

func (f *localFile) String() string {
	return fmt.Sprintf("localFile{name=%s}", f.Name())
}

var _ billy.File = (*localFile)(nil)

func newLocalFile(file *os.File) *localFile {
	return &localFile{file: file}
}

func (f *localFile) Name() string {
	return f.file.Name()
}

func (f *localFile) Write(p []byte) (n int, err error) {
	return f.file.Write(p)
}

func (f *localFile) Read(p []byte) (n int, err error) {
	return f.file.Read(p)
}

func (f *localFile) ReadAt(p []byte, off int64) (n int, err error) {
	return f.file.ReadAt(p, off)
}

func (f *localFile) Seek(offset int64, whence int) (int64, error) {
	return f.file.Seek(offset, whence)
}

func (f *localFile) Close() error {
	return f.file.Close()
}

func (f *localFile) Lock() error {
	return syscall.Flock(int(f.file.Fd()), syscall.LOCK_EX)
}

func (f *localFile) Unlock() error {
	return syscall.Flock(int(f.file.Fd()), syscall.LOCK_UN)
}

func (f *localFile) Truncate(size int64) error {
	return f.file.Truncate(size)
}
