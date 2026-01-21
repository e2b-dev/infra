package gcs

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"github.com/willscott/go-nfs/file"
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

	return 0o666
}

func (f fileInfo) ModTime() time.Time {
	return f.attrs.Updated
}

func (f fileInfo) IsDir() bool {
	return false
}

func (f fileInfo) Sys() any {
	return toFileInfo(f.attrs)
}

func toFileInfo(attrs *storage.ObjectAttrs) any {
	uid := fromMetadataToUID(attrs.Metadata)
	gid := fromMetadataToGID(attrs.Metadata)

	return &file.FileInfo{
		Nlink:  0,
		UID:    uid,
		GID:    gid,
		Major:  0,
		Minor:  0,
		Fileid: uint64(attrs.Generation),
	}
}

var _ os.FileInfo = (*fileInfo)(nil)
