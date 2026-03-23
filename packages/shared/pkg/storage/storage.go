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
	meter  = otel.Meter("github.com/e2b-dev/infra/packages/shared/pkg/storage")
)

var ErrObjectNotExist = errors.New("object does not exist")

// ErrObjectRateLimited means per-object mutation rate limiting —
// multiple concurrent writers racing to write the same content-addressed object.
var ErrObjectRateLimited = errors.New("object access rate limited")

type Provider string

const (
	GCPStorageProvider   Provider = "GCPBucket"
	AWSStorageProvider   Provider = "AWSBucket"
	LocalStorageProvider Provider = "Local"

	DefaultStorageProvider Provider = GCPStorageProvider

	storageProviderEnv = "STORAGE_PROVIDER"

	// MemoryChunkSize must always be bigger or equal to the block size.
	MemoryChunkSize = 4 * 1024 * 1024 // 4 MB

	// MetadataKeyUncompressedSize stores the original size so that Size()
	// returns the uncompressed size for compressed objects.
	MetadataKeyUncompressedSize = "uncompressed-size"
)

// RangeReadFunc is a callback for reading a byte range from storage.
type RangeReadFunc func(ctx context.Context, offset int64, length int) (io.ReadCloser, error)

// GetProviderType returns the configured storage provider type from the
// STORAGE_PROVIDER environment variable, defaulting to GCPBucket.
func GetProviderType() Provider {
	return Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))
}

// IsLocal reports whether the configured storage provider is the local
// filesystem backend.
func IsLocal() bool {
	return GetProviderType() == LocalStorageProvider
}

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

// FramedFile supports frame-based reads and compressed/uncompressed uploads.
type FramedFile interface {
	// GetFrame reads a single frame into buf. nil frameTable = uncompressed read.
	// readSize is the number of uncompressed bytes to fetch (the chunker typically
	// passes its block size so each progressive callback covers at least one block).
	// onRead is an optional progressive callback invoked as decompressed bytes
	// become available — the chunker uses this to mark mmap regions as cached
	// before the full frame is fetched, enabling concurrent readers to proceed.
	GetFrame(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool,
		buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error)

	// Size returns the uncompressed size of the object.
	Size(ctx context.Context) (int64, error)

	// StoreFile uploads a local file. When cfg is non-nil, compresses and
	// returns the FrameTable + SHA-256 checksum of compressed data.
	StoreFile(ctx context.Context, path string, cfg *CompressConfig) (*FrameTable, [32]byte, error)
}

// StorageConfig holds the configuration for creating a storage provider.
// Both GetLocalBasePath and GetBucketName are evaluated lazily so that
// callers who set environment variables at runtime (e.g. via os.Setenv
// or t.Setenv in tests) see their overrides respected.
type StorageConfig struct {
	GetLocalBasePath func() string
	GetBucketName    func() string
	limiter          *limit.Limiter
	uploadBaseURL    string
	hmacKey          []byte
}

// WithLimiter returns a copy of the config with the given limiter set.
func (c StorageConfig) WithLimiter(limiter *limit.Limiter) StorageConfig {
	c.limiter = limiter

	return c
}

// WithLocalUpload returns a copy of the config with the given local upload
// parameters set. These are only used when STORAGE_PROVIDER=Local to let the
// filesystem storage provider generate signed URLs for file uploads.
func (c StorageConfig) WithLocalUpload(uploadBaseURL string, hmacKey []byte) StorageConfig {
	c.uploadBaseURL = uploadBaseURL
	c.hmacKey = hmacKey

	return c
}

var TemplateStorageConfig = StorageConfig{
	GetLocalBasePath: func() string {
		return env.GetEnv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", "/tmp/templates")
	},
	GetBucketName: func() string {
		return utils.RequiredEnv("TEMPLATE_BUCKET_NAME", "Bucket for storing template files")
	},
}

var BuildCacheStorageConfig = StorageConfig{
	GetLocalBasePath: func() string {
		return env.GetEnv("LOCAL_BUILD_CACHE_STORAGE_BASE_PATH", "/tmp/build-cache")
	},
	GetBucketName: func() string {
		return utils.RequiredEnv("BUILD_CACHE_BUCKET_NAME", "Bucket for storing build cache files")
	},
}

func GetStorageProvider(ctx context.Context, cfg StorageConfig) (StorageProvider, error) {
	provider := GetProviderType()

	if provider == LocalStorageProvider {
		return newFileSystemStorage(cfg), nil
	}

	bucketName := cfg.GetBucketName()

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return newAWSStorage(ctx, bucketName)
	case GCPStorageProvider:
		return NewGCP(ctx, bucketName, cfg.limiter)
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

// ReadFrame is the shared implementation for reading a single frame from storage.
// Each backend (GCP, AWS, FS) calls this with their own rangeRead callback.
// Exported for use by CLI tools (inspect-build) and tests that
// need to read frames outside the normal StorageProvider stack.
func ReadFrame(ctx context.Context, rangeRead RangeReadFunc, storageDetails string, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	// Resolve fetch coordinates: for uncompressed data (nil frameTable) they
	// map 1:1; for compressed data we translate U → C via the frame table.
	var (
		fetchOffset int64
		fetchSize   int
		expectedOut int // bytes the caller should receive on success
	)

	compressed := frameTable.IsCompressed()
	if !compressed {
		fetchOffset = offsetU
		fetchSize = len(buf)
		expectedOut = len(buf)
	} else {
		frameStart, frameSize, err := frameTable.FrameFor(offsetU)
		if err != nil {
			return Range{}, fmt.Errorf("get frame for offset %#x, %s: %w", offsetU, storageDetails, err)
		}

		expectedOut = int(frameSize.C)
		if decompress {
			expectedOut = int(frameSize.U)
		}
		if len(buf) < expectedOut {
			return Range{}, fmt.Errorf("buffer too small: got %d bytes, need %d bytes for frame", len(buf), expectedOut)
		}

		fetchOffset = frameStart.C
		fetchSize = int(frameSize.C)
	}

	respBody, err := rangeRead(ctx, fetchOffset, fetchSize)
	if err != nil {
		return Range{}, fmt.Errorf("reading at %#x from %s: %w", fetchOffset, storageDetails, err)
	}
	defer respBody.Close()

	var r Range

	// No decompression needed: stream raw bytes (uncompressed or compressed passthrough).
	if !compressed || !decompress {
		r, err = readInto(respBody, buf, fetchSize, fetchOffset, readSize, onRead)
	} else {
		r, err = readFrameDecompress(respBody, frameTable, offsetU, fetchOffset, buf, readSize, onRead)
	}

	if err != nil {
		return r, err
	}

	// All sizes are known upfront (from header/frame table), so a short read
	// always indicates truncation or corruption — never a valid result.
	if r.Length != expectedOut {
		return r, fmt.Errorf("incomplete ReadFrame from %s: got %d bytes, expected %d (offset %#x)", storageDetails, r.Length, expectedOut, offsetU)
	}

	return r, nil
}

// readFrameDecompress handles the decompress=true path for compressed frames.
func readFrameDecompress(respBody io.Reader, frameTable *FrameTable, offsetU, fetchOffset int64, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	_, frameSize, _ := frameTable.FrameFor(offsetU) // already validated by caller

	switch frameTable.CompressionType() {
	case CompressionLZ4:
		cbuf := make([]byte, frameSize.C)

		_, err := io.ReadFull(respBody, cbuf)
		if err != nil {
			return Range{}, fmt.Errorf("reading compressed lz4 frame: %w", err)
		}

		out, err := DecompressLZ4(cbuf, buf[:frameSize.U])
		if err != nil {
			return Range{}, err
		}
		if onRead != nil {
			onRead(int64(len(out)))
		}

		return Range{Start: fetchOffset, Length: len(out)}, nil

	case CompressionZstd:
		dec, err := getZstdDecoder(respBody)
		if err != nil {
			return Range{}, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		defer putZstdDecoder(dec)

		return readInto(dec, buf, int(frameSize.U), fetchOffset, readSize, onRead)

	default:
		return Range{}, fmt.Errorf("unsupported compression type: %s", frameTable.CompressionType())
	}
}

// minProgressiveReadSize is the floor for progressive reads to avoid
// tiny I/O when the caller's block size is small (e.g. 4 KB rootfs).
const minProgressiveReadSize = 256 * 1024 // 256 KB

// readInto reads totalSize bytes from src into buf, returning the range read.
// When onRead is non-nil, reads in readSize-aligned blocks and calls onRead
// after each block with cumulative bytes written. When onRead is nil, reads
// all totalSize bytes at once.
func readInto(src io.Reader, buf []byte, totalSize int, rangeStart int64, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	if onRead == nil {
		n, err := io.ReadFull(src, buf[:totalSize])
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			err = nil
		}

		return Range{Start: rangeStart, Length: n}, err
	}

	readSize = max(readSize, minProgressiveReadSize)

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
