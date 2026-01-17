package gcs

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
)

func translateError(err error) error {
	if errors.Is(err, storage.ErrObjectNotExist) {
		return os.ErrNotExist
	}

	return err
}

type fileInfo struct {
	attrs *storage.ObjectAttrs
}

func (f fileInfo) Name() string {
	return f.attrs.Name
}

func (f fileInfo) Size() int64 {
	return f.attrs.Size
}

func (f fileInfo) Mode() fs.FileMode {
	if f.attrs.Metadata != nil {
		if val, ok := f.attrs.Metadata[MetadataPermsAttr]; ok {
			mode, err := strconv.ParseUint(val, 8, 32)
			if err == nil {
				return fs.FileMode(mode)
			}
		}
	}

	return 0666
}

func (f fileInfo) ModTime() time.Time {
	return f.attrs.Updated
}

func (f fileInfo) IsDir() bool {
	return false
}

func (f fileInfo) Sys() any {
	return nil
}

var _ os.FileInfo = (*fileInfo)(nil)
