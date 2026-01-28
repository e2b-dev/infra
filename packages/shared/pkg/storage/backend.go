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

	// MemoryChunkSize must always be bigger or equal to the block size.
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
	MakeMultipartUpload(ctx context.Context, objectPath string, retryConfig RetryConfig) (MultipartUploader, func(), int, error)
}

type Manager interface {
	Size(ctx context.Context, objectPath string) (int64, error)
	DeleteWithPrefix(ctx context.Context, prefix string) error
	fmt.Stringer
}

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
