package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
)

type gcsFile struct {
	p        BucketFS
	obj      *storage.ObjectHandle
	name     string
	offset   int64
	offsetMu sync.Mutex
	writer   *storage.Writer
	attrs    *storage.ObjectAttrs
}

func (f *gcsFile) String() string {
	return fmt.Sprintf("gcsFile{name=%s, offset=%d}", f.name, f.offset)
}

var _ billy.File = (*gcsFile)(nil)

func newGcsFile(p BucketFS, name string, attrs *storage.ObjectAttrs) *gcsFile {
	return &gcsFile{p: p, obj: p.bucket.Object(name), name: name, attrs: attrs}
}

func (f *gcsFile) Name() string { return f.name }

func (f *gcsFile) Write(p []byte) (n int, err error) {
	if f.offset != 0 {
		return 0, fmt.Errorf("writing from offset != 0: %w", ErrUnsupported)
	}

	if f.writer == nil {
		f.writer = f.p.bucket.Object(f.name).NewWriter(context.Background())
		f.writer.Metadata = f.attrs.Metadata
	}

	return f.writer.Write(p)
}

func (f *gcsFile) Read(p []byte) (n int, err error) {
	ctx := context.Background()

	rc, err := f.obj.NewRangeReader(ctx, f.offset, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to create range reader: %w", err)
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
	n, err = rc.Read(p)
	f.incOffset(int64(n))

	if err != nil {
		err = fmt.Errorf("failed to read from gcs file: %w", err)
	}

	return n, err
}

func (f *gcsFile) ReadAt(p []byte, off int64) (n int, err error) {
	ctx := context.Background()

	rc, err := f.obj.NewRangeReader(ctx, off, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to create range reader at offset: %w", err)
	}
	defer func() { err = errors.Join(err, rc.Close()) }()

	n, err = rc.Read(p)
	if err == nil && off+int64(n) == f.attrs.Size {
		return n, io.EOF
	}

	if err != nil {
		err = fmt.Errorf("failed to read from gcs file at offset: %w", err)
	}

	return n, err
}

func (f *gcsFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.setOffset(offset)
	case io.SeekCurrent:
		f.incOffset(offset)
	case io.SeekEnd:
		f.setOffset(f.attrs.Size + offset)
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
func (f *gcsFile) Truncate(_ int64) error {
	return fmt.Errorf("gcsFile.Truncate: %w", ErrUnsupported)
}

func (f *gcsFile) setOffset(off int64) {
	f.offsetMu.Lock()
	defer f.offsetMu.Unlock()

	f.offset = off
}

func (f *gcsFile) incOffset(n int64) {
	f.offsetMu.Lock()
	defer f.offsetMu.Unlock()

	f.offset += n
}
