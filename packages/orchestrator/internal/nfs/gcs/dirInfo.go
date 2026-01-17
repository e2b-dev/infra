package gcs

import (
	"io/fs"
	"os"
	"time"

	"cloud.google.com/go/storage"
)

type impliedDirInfo struct {
	path string
}

var _ os.FileInfo = (*impliedDirInfo)(nil)

func (i impliedDirInfo) Name() string {
	return i.path
}

func (i impliedDirInfo) Size() int64 {
	return 0
}

func (i impliedDirInfo) Mode() fs.FileMode {
	return 0777
}

func (i impliedDirInfo) ModTime() time.Time {
	return bootTime
}

func (i impliedDirInfo) IsDir() bool {
	return true
}

func (i impliedDirInfo) Sys() any {
	return nil
}

type dirInfo struct {
	attrs *storage.ObjectAttrs
}

var _ os.FileInfo = (*dirInfo)(nil)

func (d dirInfo) Name() string {
	return d.attrs.Name
}

func (d dirInfo) Size() int64 {
	return d.attrs.Size
}

func (d dirInfo) Mode() fs.FileMode {
	return fromBucketAttrs(d.attrs)
}

func (d dirInfo) ModTime() time.Time {
	return d.attrs.Updated
}

func (d dirInfo) IsDir() bool {
	return true
}

func (d dirInfo) Sys() any {
	return nil
}
