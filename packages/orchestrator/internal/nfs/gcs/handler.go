package gcs

import (
	"context"
	"net"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type NFSHandler struct {
	bucket *storage.BucketHandle
}

var _ nfs.Handler = (*NFSHandler)(nil)

func NewNFSHandler(bucket *storage.BucketHandle) *NFSHandler {
	return &NFSHandler{
		bucket: bucket,
	}
}

func (h NFSHandler) Mount(ctx context.Context, _ net.Conn, _ nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	fs := NewPrefixedGCSBucket(h.bucket)

	return nfs.MountStatusOk, fs, nil
}

func (h NFSHandler) Change(filesystem billy.Filesystem) billy.Change {
	return newChange(h.bucket, filesystem)
}

func (h NFSHandler) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) error {
	// TODO implement me
	panic("implement me")
}

func (h NFSHandler) ToHandle(fs billy.Filesystem, path []string) []byte {
	// TODO implement me
	panic("implement me")
}

func (h NFSHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	// TODO implement me
	panic("implement me")
}

func (h NFSHandler) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) error {
	// TODO implement me
	panic("implement me")
}

func (h NFSHandler) HandleLimit() int {
	// TODO implement me
	panic("implement me")
}
