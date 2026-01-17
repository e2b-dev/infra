package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Each compressed frames contains 1+ chunks.
const (
	// TODO LEV <>/<>: what should be the chunk size? Must be >= all other chunk
	// sizes to align in frames.
	defaultChunkSizeU             = 2 * megabyte // uncompressed chunk size
	defaultTargetFrameSizeC       = 4 * megabyte // target compressed frame size
	defaultZstdCompressionLevel   = zstd.SpeedBestCompression
	defaultCompressionConcurrency = 0 // use default concurrency settings
	defaultUploadConcurrency      = 8
	defaultUploadPartSize         = 50 * megabyte
)

const (
	CompressionNone = CompressionType(iota)
	CompressionZstd
	CompressionLZ4
)

type CompressionType byte

type FrameOffset struct {
	U int64
	C int64
}

type FrameSize struct {
	U int32
	C int32
}

type Range struct {
	Start  int64
	Length int
}

type FrameTable struct {
	CompressionType CompressionType
	StartAt         FrameOffset
	Frames          []FrameSize
}

type FramedUploadOptions struct {
	CompressionType        CompressionType
	Level                  int
	CompressionConcurrency int
	UploadConcurrency      int
	ChunkSize              int // frames are made of whole chunks
	TargetFrameSize        int // frames may be bigger than this due to chunk alignment and async compression.
	TargetPartSize         int
}

var DefaultCompressionOptions = &FramedUploadOptions{
	CompressionType:        CompressionZstd,
	ChunkSize:              defaultChunkSizeU,
	TargetFrameSize:        defaultTargetFrameSizeC,
	Level:                  int(defaultZstdCompressionLevel),
	CompressionConcurrency: defaultCompressionConcurrency,
	UploadConcurrency:      8,
}

type SeekableReader interface {
	io.ReaderAt
	io.Reader
}

type FrameGetter interface {
	GetFrame(ctx context.Context, path string, rangeU Range, frameTable *FrameTable, decompress bool) (Range, io.ReadCloser, error)
}

type FramedUploader interface {
	UploadFramed(ctx context.Context, asPath string, in SeekableReader, size int64, opts *FramedUploadOptions) (ft *FrameTable, err error)
}

type API interface {
	FrameGetter
	FramedUploader
	GetBlob(ctx context.Context, path string, userBuffer []byte) ([]byte, error)
	Exists(ctx context.Context, path string) (bool, error)
	Size(ctx context.Context, path string) (int64, error)
}

func GetTemplateStorage(ctx context.Context, limiter *limit.Limiter) (*Storage, error) {
	return getStorage(ctx, limiter, "LOCAL_TEMPLATE_STORAGE_BASE_PATH", "/tmp/templates", "TEMPLATE_BUCKET_NAME", "Bucket for storing template files")
}

func GetBuildStorage(ctx context.Context, limiter *limit.Limiter) (*Storage, error) {
	return getStorage(ctx, limiter, "LOCAL_BUILD_CACHE_STORAGE_BASE_PATH", "/tmp/build-cache", "BUILD_CACHE_BUCKET_NAME", "Bucket for storing build cache files")
}

func getStorage(ctx context.Context, limiter *limit.Limiter, localBaseEnv, defaultLocalBase, bucketEnv, bucketUsage string) (*Storage, error) {
	var provider *Provider
	var err error

	providerName := ProviderName(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))
	if providerName == LocalStorageProvider {
		basePath := env.GetEnv(localBaseEnv, defaultLocalBase)
		provider = NewFS(basePath)
	} else {
		bucketName := utils.RequiredEnv(bucketEnv, bucketUsage)
		provider, err = newCloudProvider(ctx, providerName, bucketName, limiter)
		if err != nil {
			return nil, err
		}
	}

	return &Storage{
		Provider: provider,
	}, nil
}

func newCloudProvider(ctx context.Context, providerName ProviderName, bucketName string, limiter *limit.Limiter) (*Provider, error) {
	var provider *Provider

	switch providerName {
	// cloud bucket-based storage
	case AWSStorageProvider:
		return NewAWS(ctx, bucketName)
	case GCPStorageProvider:
		return NewGCP(ctx, bucketName, limiter)
	default:
		return nil, fmt.Errorf("unknown storage provider: %s", provider)
	}
}

func NewGCPStorage(ctx context.Context, bucketName string, limiter *limit.Limiter) (*Storage, error) {
	provider, err := NewGCP(ctx, bucketName, limiter)
	if err != nil {
		return nil, err
	}

	return &Storage{
		Provider: provider,
	}, nil
}
