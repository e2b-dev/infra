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
	CompressionNone = iota
	CompressionZstd
	CompressionLZ4
)

type CompressionType byte

type StorageProvider interface {
	DeleteObjectsWithPrefix(ctx context.Context, prefix string) error
	UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error)
	OpenObject(ctx context.Context, path string, objectType ObjectType, compression CompressionType) (ObjectProvider, error)
	OpenSeekableObject(ctx context.Context, path string, seekableObjectType SeekableObjectType, compression CompressionType) (SeekableObjectProvider, error)
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
	WriteFromFileSystem(ctx context.Context, path string, compression CompressionType) error

	// read
	WriterToCtx

	// utility
	Exists(ctx context.Context) (bool, error)
}

type SeekableObjectProvider interface {
	// write
	WriteFromFileSystem(ctx context.Context, path string, compression CompressionType) error

	// read
	ReaderAtCtx

	// utility
	Size(ctx context.Context) (int64, error)
}

func GetTemplateStorageProvider(ctx context.Context, limiter *limit.Limiter) (StorageProvider, error) {
	provider := Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))

	if provider == LocalStorageProvider {
		basePath := env.GetEnv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", "/tmp/templates")

		return NewFileSystemStorageProvider(basePath)
	}

	bucketName := utils.RequiredEnv("TEMPLATE_BUCKET_NAME", "Bucket for storing template files")

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return NewAWSBucketStorageProvider(ctx, bucketName)
	case GCPStorageProvider:
		return NewGCPBucketStorageProvider(ctx, bucketName, limiter)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", provider)
}

func GetBuildCacheStorageProvider(ctx context.Context, limiter *limit.Limiter) (StorageProvider, error) {
	provider := Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))

	if provider == LocalStorageProvider {
		basePath := env.GetEnv("LOCAL_BUILD_CACHE_STORAGE_BASE_PATH", "/tmp/build-cache")

		return NewFileSystemStorageProvider(basePath)
	}

	bucketName := utils.RequiredEnv("BUILD_CACHE_BUCKET_NAME", "Bucket for storing template files")

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return NewAWSBucketStorageProvider(ctx, bucketName)
	case GCPStorageProvider:
		return NewGCPBucketStorageProvider(ctx, bucketName, limiter)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", provider)
}
