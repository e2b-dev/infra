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
)

var (
	tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage")
	meter  = otel.GetMeterProvider().Meter("shared.pkg.storage")
)

var ErrObjectNotExist = errors.New("object does not exist")

type ProviderName string

const (
	GCPStorageProvider   ProviderName = "GCPBucket"
	AWSStorageProvider   ProviderName = "AWSBucket"
	LocalStorageProvider ProviderName = "Local"

	DefaultStorageProvider ProviderName = GCPStorageProvider

	storageProviderEnv = "STORAGE_PROVIDER"

	// MemoryChunkSize is the unit of caching and prefetching for storage operations.
	// It must be >= all block sizes (HugepageSize=2MB, RootfsBlockSize=4KB).
	// Compression frame sizes (uncompressed) must be a multiple of this value
	// to ensure aligned chunk requests do not cross frame boundaries.
	MemoryChunkSize = 4 * 1024 * 1024 // 4 MB
)

const (
	kilobyte = 1024
	megabyte = 1024 * kilobyte
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

type Basic interface {
	Upload(ctx context.Context, objectPath string, in io.Reader) (int64, error)
	StartDownload(ctx context.Context, objectPath string) (io.ReadCloser, error)
}

type RangeGetter interface {
	RangeGet(ctx context.Context, objectPath string, offset int64, length int) (io.ReadCloser, error)
}

type PublicUploader interface {
	PublicUploadURL(ctx context.Context, objectPath string, ttl time.Duration) (string, error)
}

type MultipartUploaderFactory interface {
	MakeMultipartUpload(ctx context.Context, objectPath string, retryConfig RetryConfig, metadata map[string]string) (MultipartUploader, func(), int, error)
}

type Manager interface {
	// Size returns both virtual (uncompressed) and raw (file) sizes in one call.
	// For compressed files: virtSize is uncompressed size, rawSize is compressed file size.
	// For uncompressed files: virtSize == rawSize (actual file size).
	Size(ctx context.Context, objectPath string) (virtSize, rawSize int64, err error)
	DeleteWithPrefix(ctx context.Context, prefix string) error
	fmt.Stringer
}

// MetadataKeyUncompressedSize is the metadata key for storing uncompressed size on objects.
const MetadataKeyUncompressedSize = "e2b-uncompressed-size"

type MultipartUploader interface {
	Start(ctx context.Context) error
	UploadPart(ctx context.Context, partIndex int, data ...[]byte) error
	Complete(ctx context.Context) error
}

type Backend struct {
	Basic
	PublicUploader
	MultipartUploaderFactory
	RangeGetter
	Manager
}

func recordError(span trace.Span, err error) {
	if ignoreEOF(err) == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

func newMultiReader(data [][]byte) io.Reader {
	rr := []io.Reader{}
	for _, d := range data {
		rr = append(rr, bytes.NewReader(d))
	}

	return io.MultiReader(rr...)
}
