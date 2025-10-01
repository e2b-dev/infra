package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrObjectNotExist = errors.New("object does not exist")

// MemoryChunkSize must always be bigger or equal to the block size.
const MemoryChunkSize = 4 * 1024 * 1024 // 4 MB

type StorageProvider interface {
	DeleteObjectsWithPrefix(ctx context.Context, prefix string) error
	UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error)
	OpenObject(ctx context.Context, path string) (StorageObjectProvider, error)
	GetDetails() string
}

type WriterCtx interface {
	Write(ctx context.Context, p []byte) (n int, err error)
}

type WriterToCtx interface {
	WriteTo(ctx context.Context, w io.Writer) (n int64, err error)
}

type ReaderAtCtx interface {
	ReadAt(ctx context.Context, p []byte, off int64) (n int, err error)
}

type StorageObjectProvider interface {
	WriterCtx
	WriterToCtx
	ReaderAtCtx

	WriteFromFileSystem(ctx context.Context, path string) error

	Size(ctx context.Context) (int64, error)
	Delete(ctx context.Context) error
}
