package gcs

import (
	"context"
	"fmt"
	"net"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type NFSHandler struct {
	ctx    context.Context //nolint:containedctx // can't change the API, still need it
	bucket *storage.BucketHandle
}

func (h NFSHandler) String() string {
	return fmt.Sprintf("NFSHandler{bucket=%s}", h.bucket.BucketName())
}

var _ nfs.Handler = (*NFSHandler)(nil)

func NewNFSHandler(ctx context.Context, bucket *storage.BucketHandle) *NFSHandler {
	return &NFSHandler{
		ctx:    ctx,
		bucket: bucket,
	}
}

func (h NFSHandler) Mount(_ context.Context, _ net.Conn, _ nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	fs := NewPrefixedGCSBucket(h.bucket)

	return nfs.MountStatusOk, fs, nil
}

func (h NFSHandler) Change(filesystem billy.Filesystem) billy.Change {
	return newChange(h.ctx, h.bucket, filesystem)
}

func (h NFSHandler) FSStat(_ context.Context, _ billy.Filesystem, _ *nfs.FSStat) error {
	// TODO implement me
	panic("implement me")
}

func (h NFSHandler) ToHandle(_ billy.Filesystem, _ []string) []byte {
	// TODO implement me
	panic("implement me")
}

func (h NFSHandler) FromHandle(_ []byte) (billy.Filesystem, []string, error) {
	// TODO implement me
	panic("implement me")
}

func (h NFSHandler) InvalidateHandle(_ billy.Filesystem, _ []byte) error {
	panic("implement me")
}

func (h NFSHandler) HandleLimit() int {
	// TODO implement me
	panic("implement me")
}
