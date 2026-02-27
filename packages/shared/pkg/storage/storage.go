package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

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

// rangeReadFunc is a callback for reading a byte range from storage.
type rangeReadFunc func(ctx context.Context, offset int64, length int) (io.ReadCloser, error)

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

type StorageProvider interface {
	DeleteObjectsWithPrefix(ctx context.Context, prefix string) error
	UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error)
	OpenBlob(ctx context.Context, path string) (Blob, error)
	OpenFramedFile(ctx context.Context, path string) (FramedFile, error)
	GetDetails() string
}

type Blob interface {
	WriteTo(ctx context.Context, dst io.Writer) (int64, error)
	Put(ctx context.Context, data []byte) error
	Exists(ctx context.Context) (bool, error)
}

// FramedFile represents a storage object that supports frame-based reads.
// The object knows its own path; callers do not need to supply it.
type FramedFile interface {
	// GetFrame reads a single frame from storage into buf. When frameTable is
	// nil (uncompressed data), reads directly without frame translation. When
	// onRead is non-nil, data is written in readSize-aligned chunks and onRead
	// is called after each chunk with the cumulative byte count written so far.
	// When readSize <= 0, MemoryChunkSize is used as the default.
	GetFrame(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool,
		buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error)

	// Size returns the uncompressed size of the object. For compressed objects
	// with metadata, this returns the original uncompressed size.
	Size(ctx context.Context) (int64, error)

	// StoreFile uploads the local file at path, as a multipart upload. When
	// opts is non-nil with a compression type, compresses the data and returns
	// the FrameTable describing the compressed frames. When opts is nil,
	// performs a simple uncompressed upload (returns nil FrameTable).
	StoreFile(ctx context.Context, path string, opts *FramedUploadOptions) (*FrameTable, error)
}

func GetTemplateStorageProvider(ctx context.Context, limiter *limit.Limiter) (StorageProvider, error) {
	provider := Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))

	if provider == LocalStorageProvider {
		basePath := env.GetEnv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", "/tmp/templates")

		return newFileSystemStorage(basePath), nil
	}

	bucketName := utils.RequiredEnv("TEMPLATE_BUCKET_NAME", "Bucket for storing template files")

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return newAWSStorage(ctx, bucketName)
	case GCPStorageProvider:
		return NewGCP(ctx, bucketName, limiter)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", provider)
}

func GetBuildCacheStorageProvider(ctx context.Context, limiter *limit.Limiter) (StorageProvider, error) {
	provider := Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))

	if provider == LocalStorageProvider {
		basePath := env.GetEnv("LOCAL_BUILD_CACHE_STORAGE_BASE_PATH", "/tmp/build-cache")

		return newFileSystemStorage(basePath), nil
	}

	bucketName := utils.RequiredEnv("BUILD_CACHE_BUCKET_NAME", "Bucket for storing template files")

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return newAWSStorage(ctx, bucketName)
	case GCPStorageProvider:
		return NewGCP(ctx, bucketName, limiter)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", provider)
}

func recordError(span trace.Span, err error) {
	if ignoreEOF(err) == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// GetBlob is a convenience wrapper that wraps b.WriteTo interface to return a
// byte slice.
func GetBlob(ctx context.Context, b Blob) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := b.WriteTo(ctx, &buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// LoadBlob opens a blob by path and reads its contents.
func LoadBlob(ctx context.Context, s StorageProvider, path string) ([]byte, error) {
	blob, err := s.OpenBlob(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to open blob %s: %w", path, err)
	}

	return GetBlob(ctx, blob)
}

// getFrame is the shared implementation for reading a single frame from storage.
// Each backend (GCP, AWS, FS) calls this with their own rangeRead callback.
//
// When onRead is non-nil, the output is written to buf in readSize-aligned
// blocks and onRead is called after each block with the cumulative bytes
// written. This pipelines network I/O with decompression — the LZ4/zstd reader
// pulls compressed bytes from the HTTP stream on demand, so fetch and decompress
// overlap naturally. When readSize <= 0, MemoryChunkSize is used.
func getFrame(ctx context.Context, rangeRead rangeReadFunc, storageDetails string, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	// Handle uncompressed data (nil frameTable) - read directly without frame translation
	if !IsCompressed(frameTable) {
		return getFrameUncompressed(ctx, rangeRead, storageDetails, offsetU, buf, readSize, onRead)
	}

	// Get the frame info: translate U offset -> C offset for fetching
	frameStart, frameSize, err := frameTable.FrameFor(offsetU)
	if err != nil {
		return Range{}, fmt.Errorf("get frame for offset %#x, %s: %w", offsetU, storageDetails, err)
	}

	// Validate buffer size
	expectedSize := int(frameSize.C)
	if decompress {
		expectedSize = int(frameSize.U)
	}
	if len(buf) < expectedSize {
		return Range{}, fmt.Errorf("buffer too small: got %d bytes, need %d bytes for frame", len(buf), expectedSize)
	}

	// Fetch the compressed data from storage
	respBody, err := rangeRead(ctx, frameStart.C, int(frameSize.C))
	if err != nil {
		return Range{}, fmt.Errorf("getting frame at %#x from %s: %w", frameStart.C, storageDetails, err)
	}
	defer respBody.Close()

	var from io.Reader = respBody
	totalSize := int(frameSize.C)

	if decompress {
		totalSize = int(frameSize.U)

		switch frameTable.CompressionType {
		case CompressionZstd:
			dec, err := getZstdDecoder(respBody)
			if err != nil {
				return Range{}, fmt.Errorf("failed to create zstd decoder: %w", err)
			}
			defer putZstdDecoder(dec)
			from = dec

		case CompressionLZ4:
			rd := getLZ4Reader(respBody)
			defer putLZ4Reader(rd)
			from = rd

		default:
			return Range{}, fmt.Errorf("unsupported compression type: %s", frameTable.CompressionType)
		}
	}

	// Progressive mode: read in readSize blocks, call onRead after each.
	if onRead != nil {
		return readProgressive(from, buf, totalSize, frameStart.C, readSize, onRead)
	}

	n, err := io.ReadFull(from, buf[:totalSize])

	return Range{Start: frameStart.C, Length: n}, err
}

// readProgressive reads from src into buf in readSize-aligned blocks,
// calling onRead after each block with the cumulative bytes written.
// When readSize <= 0, MemoryChunkSize is used as the default.
func readProgressive(src io.Reader, buf []byte, totalSize int, rangeStart int64, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	if readSize <= 0 {
		readSize = MemoryChunkSize
	}

	var total int64

	for total < int64(totalSize) {
		end := min(total+readSize, int64(totalSize))
		n, err := io.ReadFull(src, buf[total:end])
		total += int64(n)

		if int64(n) > 0 {
			onRead(total)
		}

		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}

		if err != nil {
			return Range{}, fmt.Errorf("progressive read error after %d bytes: %w", total, err)
		}
	}

	return Range{Start: rangeStart, Length: int(total)}, nil
}

// getFrameUncompressed reads uncompressed data directly from storage.
// When onRead is non-nil, uses readProgressive for progressive delivery.
func getFrameUncompressed(ctx context.Context, rangeRead rangeReadFunc, storageDetails string, offset int64, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	respBody, err := rangeRead(ctx, offset, len(buf))
	if err != nil {
		return Range{}, fmt.Errorf("getting uncompressed data at %#x from %s: %w", offset, storageDetails, err)
	}
	defer respBody.Close()

	if onRead != nil {
		return readProgressive(respBody, buf, len(buf), offset, readSize, onRead)
	}

	n, err := io.ReadFull(respBody, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Range{}, fmt.Errorf("reading uncompressed data from %s: %w", storageDetails, err)
	}

	return Range{Start: offset, Length: n}, nil
}
