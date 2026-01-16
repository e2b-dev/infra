package nfs

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	return p.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

func (p prefixedGCSBucket) Open(filename string) (billy.File, error) {
	return p.OpenFile(filename, os.O_RDONLY, 0)
}

func (p prefixedGCSBucket) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	if err := p.requirePrefix(filename); err != nil {
		return nil, err
	}

	f := &gcsFile{
		p:    p,
		name: filename,
		flag: flag,
	}

	if flag&os.O_CREATE != 0 {
		if flag&os.O_EXCL != 0 {
			_, err := p.bucket.Object(filename).Attrs(p.ctx)
			if err == nil {
				return nil, os.ErrExist
			}
		}
	} else {
		_, err := p.bucket.Object(filename).Attrs(p.ctx)
		if err != nil {
			return nil, err
		}
	}

	return f, nil
}

func (p prefixedGCSBucket) Stat(filename string) (os.FileInfo, error) {
	return p.Lstat(filename)
}

func (p prefixedGCSBucket) Rename(oldpath, newpath string) error {
	if err := p.requirePrefix(oldpath); err != nil {
		return err
	}
	if err := p.requirePrefix(newpath); err != nil {
		return err
	}

	src := p.bucket.Object(oldpath)
	dst := p.bucket.Object(newpath)

	if _, err := dst.CopierFrom(src).Run(p.ctx); err != nil {
		return err
	}

	return src.Delete(p.ctx)
}

func (p prefixedGCSBucket) Remove(filename string) error {
	if err := p.requirePrefix(filename); err != nil {
		return err
	}
	return p.bucket.Object(filename).Delete(p.ctx)
}

func (p prefixedGCSBucket) Join(elem ...string) string {
	elem = append([]string{p.prefix}, elem...)
	return filepath.Join(elem...)
}

func (p prefixedGCSBucket) TempFile(dir, prefix string) (billy.File, error) {
	return nil, errors.New("TempFile not implemented")
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
	return nil // GCS is an object store, directories are virtual
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
		return nil, translateError(err)
	}

	return fileInfo{attr}, nil
}

func translateError(err error) error {
	if errors.Is(err, storage.ErrObjectNotExist) {
		return os.ErrNotExist
	}

	return err
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
	return errors.New("symlink not supported")
}

func (p prefixedGCSBucket) Readlink(link string) (string, error) {
	return "", errors.New("readlink not supported")
}

func (p prefixedGCSBucket) Chroot(path string) (billy.Filesystem, error) {
	if err := p.requirePrefix(path); err != nil {
		return nil, err
	}
	return &prefixedGCSBucket{
		ctx:    p.ctx,
		bucket: p.bucket,
		prefix: path,
	}, nil
}

func (p prefixedGCSBucket) Root() string {
	return p.prefix
}

type gcsFile struct {
	p      prefixedGCSBucket
	name   string
	flag   int
	offset int64
	writer *storage.Writer
}

func (f *gcsFile) Name() string { return f.name }

func (f *gcsFile) Write(p []byte) (n int, err error) {
	if f.flag&os.O_RDONLY != 0 {
		return 0, errors.New("file is read-only")
	}
	if f.writer == nil {
		f.writer = f.p.bucket.Object(f.name).NewWriter(f.p.ctx)
	}
	return f.writer.Write(p)
}

func (f *gcsFile) Read(p []byte) (n int, err error) {
	rc, err := f.p.bucket.Object(f.name).NewRangeReader(f.p.ctx, f.offset, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	n, err = rc.Read(p)
	f.offset += int64(n)
	return n, err
}

func (f *gcsFile) ReadAt(p []byte, off int64) (n int, err error) {
	rc, err := f.p.bucket.Object(f.name).NewRangeReader(f.p.ctx, off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return rc.Read(p)
}

func (f *gcsFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		attr, err := f.p.bucket.Object(f.name).Attrs(f.p.ctx)
		if err != nil {
			return 0, err
		}
		f.offset = attr.Size + offset
	}
	return f.offset, nil
}

func (f *gcsFile) Close() error {
	if f.writer != nil {
		return f.writer.Close()
	}
	return nil
}

func (f *gcsFile) Lock() error   { return nil }
func (f *gcsFile) Unlock() error { return nil }
func (f *gcsFile) Truncate(size int64) error {
	return errors.New("truncate not supported")
}
