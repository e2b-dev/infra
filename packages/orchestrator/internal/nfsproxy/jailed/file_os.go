package jailed

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

type jailedFile struct {
	inner  os.FileInfo
	prefix string
}

func (j jailedFile) String() string {
	return fmt.Sprintf("jailedFile{name=%s, prefix=%s, size=%d}", j.Name(), j.prefix, j.Size())
}

var _ os.FileInfo = (*jailedFile)(nil)

func wrapOSFile(item os.FileInfo, prefix string) os.FileInfo {
	return &jailedFile{inner: item, prefix: prefix}
}

func (j jailedFile) Name() string {
	name := j.inner.Name()

	return strings.TrimPrefix(name, j.prefix)
}

func (j jailedFile) Size() int64 {
	return j.inner.Size()
}

func (j jailedFile) Mode() fs.FileMode {
	return j.inner.Mode()
}

func (j jailedFile) ModTime() time.Time {
	return j.inner.ModTime()
}

func (j jailedFile) IsDir() bool {
	return j.inner.IsDir()
}

func (j jailedFile) Sys() any {
	return j.inner.Sys()
}
