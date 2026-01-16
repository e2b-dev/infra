package nfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
	"google.golang.org/api/iterator"
)

type prefixedGCSBucket struct {
	ctx    context.Context
	prefix string
	bucket *storage.BucketHandle
}

var _ billy.Filesystem = (*prefixedGCSBucket)(nil)

func newPrefixedGCSBucket(ctx context.Context, client *storage.Client, bucketName, prefix string) *prefixedGCSBucket {
	return &prefixedGCSBucket{ctx: ctx, bucket: client.Bucket(bucketName), prefix: prefix}
}

func (p prefixedGCSBucket) Create(filename string) (billy.File, error) {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Open(filename string) (billy.File, error) {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Stat(filename string) (os.FileInfo, error) {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Rename(oldpath, newpath string) error {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Remove(filename string) error {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Join(elem ...string) string {
	elem = append([]string{p.prefix}, elem...)
	return filepath.Join(elem...)
}

func (p prefixedGCSBucket) TempFile(dir, prefix string) (billy.File, error) {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) ReadDir(path string) ([]os.FileInfo, error) {
	objects := p.bucket.Objects(p.ctx, &storage.Query{Prefix: path + "/"})

	var results []os.FileInfo
	for {
		object, err := objects.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("error when iterating over template object: %w", err)
		}

		results = append(results, fileInfo{object})
	}

	return results, nil
}

func (p prefixedGCSBucket) MkdirAll(filename string, perm os.FileMode) error {
	//TODO implement me
	panic("implement me")
}

var ErrInvalidPrefix = errors.New("invalid prefix")

func (p prefixedGCSBucket) requirePrefix(path string) error {
	if !strings.HasPrefix(path, p.prefix) {
		return ErrInvalidPrefix
	}

	return nil
}

func (p prefixedGCSBucket) Lstat(filename string) (os.FileInfo, error) {
	if err := p.requirePrefix(filename); err != nil {
		return nil, err
	}

	if filename == p.prefix {
		return rootListing{}, nil
	}

	attr, err := p.bucket.Object(filename).Attrs(p.ctx)
	if err != nil {
		return nil, err
	}

	return fileInfo{attr}, nil
}

type rootListing struct {
}

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
	return bootTime
}

func (r rootListing) IsDir() bool {
	return true
}

func (r rootListing) Sys() any {
	return nil
}

var _ os.FileInfo = (*rootListing)(nil)

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
	return 0
}

func (f fileInfo) ModTime() time.Time {
	return f.attrs.Updated
}

func (f fileInfo) IsDir() bool {
	return false
}

func (f fileInfo) Sys() any {
	return f.attrs
}

var _ os.FileInfo = (*fileInfo)(nil)

func (p prefixedGCSBucket) Symlink(target, link string) error {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Readlink(link string) (string, error) {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Chroot(path string) (billy.Filesystem, error) {
	//TODO implement me
	panic("implement me")
}

func (p prefixedGCSBucket) Root() string {
	//TODO implement me
	panic("implement me")
}
