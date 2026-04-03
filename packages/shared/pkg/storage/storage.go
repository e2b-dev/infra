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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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
	OpenBlob(ctx context.Context, path string, objectType ObjectType) (Blob, error)
	OpenSeekable(ctx context.Context, path string, seekableObjectType SeekableObjectType) (Seekable, error)
	GetDetails() string
}

type Blob interface {
	WriteTo(ctx context.Context, dst io.Writer) (int64, error)
	Put(ctx context.Context, data []byte) error
	Exists(ctx context.Context) (bool, error)
}

type SeekableReader interface {
	// Random slice access, off and buffer length must be aligned to block size
	ReadAt(ctx context.Context, buffer []byte, off int64, ft *FrameTable) (int, error)
	Size(ctx context.Context) (int64, error)
}

// StreamingReader supports progressive reads via a streaming range reader.
type StreamingReader interface {
	OpenRangeReader(ctx context.Context, offsetU int64, length int64, frameTable *FrameTable) (io.ReadCloser, error)
}

type SeekableWriter interface {
	// Store entire file
	StoreFile(ctx context.Context, path string, cfg *CompressConfig) (*FrameTable, [32]byte, error)
}

type Seekable interface {
	StreamingReader
	SeekableWriter
	Size(ctx context.Context) (int64, error)
}

// PeerTransitionedError is returned by the peer Seekable when the GCS upload
// has completed and serialized V4 headers are available.
type PeerTransitionedError struct {
	MemfileHeader []byte
	RootfsHeader  []byte
}

func (e *PeerTransitionedError) Error() string {
	return "peer upload completed, headers available"
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
func LoadBlob(ctx context.Context, s StorageProvider, path string, objectType ObjectType) ([]byte, error) {
	blob, err := s.OpenBlob(ctx, path, objectType)
	if err != nil {
		return nil, fmt.Errorf("failed to open blob %s: %w", path, err)
	}

	return GetBlob(ctx, blob)
}

// timedReadCloser wraps a reader with OTEL timer metrics.
// Close records success (with total bytes read) or failure on the timer.
type timedReadCloser struct {
	inner     io.ReadCloser
	timer     *telemetry.Stopwatch
	ctx       context.Context //nolint:containedctx // needed for timer recording in Close
	bytesRead int64
	closeErr  error
}

func (r *timedReadCloser) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.bytesRead += int64(n)

	if err != nil && err != io.EOF {
		r.closeErr = err
	}

	return n, err
}

func (r *timedReadCloser) Close() error {
	err := r.inner.Close()

	if r.closeErr != nil || err != nil {
		r.timer.Failure(r.ctx, r.bytesRead)
	} else {
		r.timer.Success(r.ctx, r.bytesRead)
	}

	return err
}
