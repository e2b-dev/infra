package jailed

import (
	"fmt"

	"github.com/go-git/go-billy/v5"
)

type jailedBillyFile struct {
	inner  billy.File
	prefix string
}

func (j jailedBillyFile) String() string {
	return fmt.Sprintf("jailedBillyFile{name=%s, prefix=%s, inner=%v}", j.Name(), j.prefix, j.inner)
}

var _ billy.File = (*jailedBillyFile)(nil)

func tryWrapBillyFile(f billy.File, prefix string) billy.File {
	if f == nil {
		return nil
	}

	return &jailedBillyFile{inner: f, prefix: prefix}
}

func (j jailedBillyFile) Name() string {
	return j.inner.Name()
}

func (j jailedBillyFile) Write(p []byte) (n int, err error) {
	return j.inner.Write(p)
}

func (j jailedBillyFile) Read(p []byte) (n int, err error) {
	return j.inner.Read(p)
}

func (j jailedBillyFile) ReadAt(p []byte, off int64) (n int, err error) {
	return j.inner.ReadAt(p, off)
}

func (j jailedBillyFile) Seek(offset int64, whence int) (int64, error) {
	return j.inner.Seek(offset, whence)
}

func (j jailedBillyFile) Close() error {
	return j.inner.Close()
}

func (j jailedBillyFile) Lock() error {
	return j.inner.Lock()
}

func (j jailedBillyFile) Unlock() error {
	return j.inner.Unlock()
}

func (j jailedBillyFile) Truncate(size int64) error {
	return j.inner.Truncate(size)
}
