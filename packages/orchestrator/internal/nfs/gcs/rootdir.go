package gcs

import (
	"io/fs"
	"os"
	"time"
)

type rootDir struct{}

func (r rootDir) Name() string {
	return ""
}

func (r rootDir) Size() int64 {
	return 0
}

func (r rootDir) Mode() fs.FileMode {
	return fs.ModeDir
}

var bootTime = time.Now()

func (r rootDir) ModTime() time.Time {
	return bootTime // if this is dynamic, dir listings never return
}

func (r rootDir) IsDir() bool {
	return true
}

func (r rootDir) Sys() any {
	return nil
}

var _ os.FileInfo = (*rootDir)(nil)
