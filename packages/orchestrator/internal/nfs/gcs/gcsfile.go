package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
)

type gcsFile struct {
	p      BucketFS
	obj    *storage.ObjectHandle
	name   string
	offset int64
	writer *storage.Writer
	attrs  *storage.ObjectAttrs
}

func (f *gcsFile) String() string {
	return fmt.Sprintf("gcsFile{name=%s, offset=%d}", f.name, f.offset)
}

var _ billy.File = (*gcsFile)(nil)

func newGcsFile(p BucketFS, name string, attrs *storage.ObjectAttrs) *gcsFile {
	return &gcsFile{p, p.bucket.Object(name), name, 0, nil, attrs}
}

func (f *gcsFile) Name() string { return f.name }

func (f *gcsFile) Write(p []byte) (n int, err error) {
	if f.writer == nil {
		f.writer = f.p.bucket.Object(f.name).NewWriter(context.Background())
		f.writer.Metadata = f.attrs.Metadata
	}

	return f.writer.Write(p)
}

func (f *gcsFile) Read(p []byte) (n int, err error) {
	rc, err := f.obj.NewRangeReader(context.Background(), f.offset, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
	n, err = rc.Read(p)
	f.offset += int64(n)

	return n, err
}

func (f *gcsFile) ReadAt(p []byte, off int64) (n int, err error) {
	rc, err := f.p.bucket.Object(f.name).NewRangeReader(context.Background(), off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()

	n, err = rc.Read(p)
	if err == nil && off+int64(n) == f.attrs.Size {
		err = io.EOF
	}

	return n, err
}

func (f *gcsFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		attr, err := f.p.bucket.Object(f.name).Attrs(context.Background())
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
