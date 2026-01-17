package gcs

import (
	"errors"
	"io"

	"cloud.google.com/go/storage"
)

type gcsFile struct {
	p        BucketFS
	name     string
	offset   int64
	writer   *storage.Writer
	metadata map[string]string
}

func newGcsFile(p BucketFS, name string, metadata map[string]string) *gcsFile {
	return &gcsFile{p, name, 0, nil, metadata}
}

func (f *gcsFile) Name() string { return f.name }

func (f *gcsFile) Write(p []byte) (n int, err error) {
	if f.writer == nil {
		f.writer = f.p.bucket.Object(f.name).NewWriter(f.p.ctx)
		f.writer.Metadata = f.metadata
	}
	return f.writer.Write(p)
}

func (f *gcsFile) Read(p []byte) (n int, err error) {
	rc, err := f.p.bucket.Object(f.name).NewRangeReader(f.p.ctx, f.offset, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
	n, err = rc.Read(p)
	f.offset += int64(n)
	return n, err
}

func (f *gcsFile) ReadAt(p []byte, off int64) (n int, err error) {
	rc, err := f.p.bucket.Object(f.name).NewRangeReader(f.p.ctx, off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
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
