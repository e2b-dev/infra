package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage")
	meter  = otel.GetMeterProvider().Meter("shared.pkg.storage")
)

var ErrObjectNotExist = errors.New("object does not exist")

type Provider string

const (
	GCPStorageProvider   Provider = "GCPBucket"
	AWSStorageProvider   Provider = "AWSBucket"
	LocalStorageProvider Provider = "Local"

	DefaultStorageProvider Provider = GCPStorageProvider

	storageProviderEnv = "STORAGE_PROVIDER"

	// MemoryChunkSize must always be bigger or equal to the block size.
	MemoryChunkSize = 4 * 1024 * 1024 // 4 MB
)

type SeekableObjectType int

const (
	UnknownSeekableObjectType SeekableObjectType = iota
	MemfileObjectType
	RootFSObjectType
)

type ObjectType int

const (
	UnknownObjectType ObjectType = iota
	MemfileHeaderObjectType
	RootFSHeaderObjectType
	SnapfileObjectType
	MetadataObjectType
	BuildLayerFileObjectType
	LayerMetadataObjectType
)

const (
	CompressionNone = CompressionType(iota)
	CompressionZstd
	CompressionLZ4
)

type CompressionType byte

type Offset struct {
	U int64 // TODO is uint64 needed?
	C int64
}

type Frame struct {
	U int
	C int
}

type CompressedInfo struct {
	CompressionType CompressionType
	FramesStartAt   Offset
	Frames          []Frame
}

type CompressionOptions struct {
	CompressionType CompressionType
	Level           int
	Concurrency     int
	ChunkSize       int // frames are made of whole chunks
	TargetFrameSize int // frames may be bigger than this size due to chunk alignment and async compression.
}

type StorageProvider interface {
	DeleteObjectsWithPrefix(ctx context.Context, prefix string) error
	UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error)
	OpenObject(ctx context.Context, path string, objectType ObjectType) (ObjectProvider, error)
	OpenFramedWriter(ctx context.Context, path string, opts *CompressionOptions) (FramedWriter, error)
	OpenFramedReader(ctx context.Context, path string, compressedInfo *CompressedInfo) (FramedReader, error)
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

type ObjectProvider interface {
	// write
	WriterCtx
	CopyFromFileSystem(ctx context.Context, path string) error

	// read
	WriterToCtx

	// utility
	Exists(ctx context.Context) (bool, error)
}

type FramedWriter interface {
	StoreFromFileSystem(ctx context.Context, path string) (*CompressedInfo, error)
}

type FramedReader interface {
	// read
	ReaderAtCtx

	// utility
	Size(ctx context.Context) (int64, error)
}

func GetTemplateStorageProvider(ctx context.Context, limiter *limit.Limiter) (StorageProvider, error) {
	provider := Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))

	if provider == LocalStorageProvider {
		basePath := env.GetEnv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", "/tmp/templates")

		return newFSStore(basePath)
	}

	bucketName := utils.RequiredEnv("TEMPLATE_BUCKET_NAME", "Bucket for storing template files")

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return newAWSBucketStore(ctx, bucketName)
	case GCPStorageProvider:
		return newGCPBucketStore(ctx, bucketName, limiter)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", provider)
}

func GetBuildCacheStorageProvider(ctx context.Context, limiter *limit.Limiter) (StorageProvider, error) {
	provider := Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))

	if provider == LocalStorageProvider {
		basePath := env.GetEnv("LOCAL_BUILD_CACHE_STORAGE_BASE_PATH", "/tmp/build-cache")

		return newFSStore(basePath)
	}

	bucketName := utils.RequiredEnv("BUILD_CACHE_BUCKET_NAME", "Bucket for storing template files")

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return newAWSBucketStore(ctx, bucketName)
	case GCPStorageProvider:
		return newGCPBucketStore(ctx, bucketName, limiter)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", provider)
}
