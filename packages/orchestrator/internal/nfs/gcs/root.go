package gcs

import (
	"io/fs"
	"os"
	"time"
)

type rootListing struct{}

func (r rootListing) Name() string {
	return ""
}

func (r rootListing) Size() int64 {
	return 0
}

func (r rootListing) Mode() fs.FileMode {
	return fs.ModeDir
}

var bootTime = time.Now()

func (r rootListing) ModTime() time.Time {
	return bootTime // if this is dynamic, dir listings never return
}

func (r rootListing) IsDir() bool {
	return true
}

func (r rootListing) Sys() any {
	return nil
}

var _ os.FileInfo = (*rootListing)(nil)
