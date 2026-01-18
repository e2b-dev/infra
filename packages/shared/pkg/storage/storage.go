package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"

	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"

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
}

type ReaderAt interface {
	io.ReaderAt
	ReadAtCtx(ctx context.Context, p []byte, off int64) (n int, err error)
}

type Reader interface {
	io.Reader
	ReadCtx(ctx context.Context, p []byte) (n int, err error)
}

type AnyReader interface {
	ReaderAt
	Reader
}

type FrameGetter interface {
	GetFrame(ctx context.Context, path string, rangeU Range, frameTable *FrameTable, decompress bool) (Range, io.ReadCloser, error)
}

type Storer interface {
	Store(ctx context.Context, asPath string, in io.Reader, size int64, opts *FramedUploadOptions) (ft *FrameTable, err error)
}

// TODO LEV <>/<> do away with the API type and use functions(Provider?)
type API interface {
	// Provider-independent higher level APIs
	FrameGetter
	Storer
	GetBlob(ctx context.Context, path string, userBuffer []byte) ([]byte, error)
}

type Storage struct {
	*Provider

	uploadLimiter *utils.AdjustableSemaphore
}

var _ API = (*Storage)(nil)

// UploadFileFramed compresses the given file and uploads it using multipart
// upload. If the compression type is unset, the file is uploaded in its
// entirety.
func (s *Storage) Store(ctx context.Context, asPath string, in io.Reader, sizeU int64, opts *FramedUploadOptions) (ft *FrameTable, e error) {
	ctx, span := tracer.Start(ctx, "store file")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	compression := CompressionNone
	partSize := defaultUploadPartSize
	if opts != nil {
		compression = opts.CompressionType
		if opts.TargetPartSize > 0 {
			partSize = opts.TargetPartSize
		}
	}

	readerAt, canReadAt := in.(io.ReaderAt)
	if compression == CompressionNone && (!canReadAt || s.Provider.MultipartUploaderFactory == nil || sizeU <= int64(partSize)) {
		// If not using multipart or compressed upload, fall through to simple put.
		return nil, s.Provider.Put(ctx, asPath, in)
	}

	timer := googleWriteTimerFactory.Begin(
		attribute.String(gcsOperationAttr, gcsOperationAttrStore))

	partUploader, cleanup, maxConcurrency, err := s.Provider.MakeMultipartUpload(ctx, asPath, DefaultRetryConfig())
	defer cleanup()
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, fmt.Errorf("failed to initiate upload: %w", err)
	}

	switch {
	case compression != CompressionNone:
		ft, err = newFrameEncoder(opts, partUploader, maxConcurrency).uploadFramed(ctx, asPath, in)

	case canReadAt && s.Provider.MultipartUploaderFactory != nil && sizeU > int64(partSize):
		err = uploadFileInParallel(ctx, asPath, readerAt, sizeU, partUploader, partSize, maxConcurrency)

	default:
		return nil, s.Provider.Put(ctx, asPath, in)
	}
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, err
	}

	timer.Success(ctx, sizeU)

	return ft, err
}

// See convenience function GetFrameData() that takes an arbitrary offset/length
// range and a frameTable; then returns the uncompressed []byte for the frame
// that contains the region, or an error.
func (s *Storage) GetFrame(ctx context.Context, path string, rangeU Range, frameTable *FrameTable, decompress bool) (Range, io.ReadCloser, error) {
	fetchRange := rangeU
	if frameTable != nil && frameTable.CompressionType != CompressionNone {
		start, size, err := frameTable.FrameFor(rangeU)
		if err != nil {
			return Range{}, nil, fmt.Errorf("getting frame for range %#x/%#x: %w", rangeU.Start, rangeU.Length, err)
		}
		fetchRange = Range{
			Start:  start.C,
			Length: int(size.C),
		}
	}

	// send out the range request
	respBody, err := s.Provider.RangeGet(ctx, path, fetchRange.Start, fetchRange.Length)
	if err != nil {
		return Range{}, nil, fmt.Errorf("getting frame at %#x from %s in %s: %w", fetchRange.Start, path, s.Provider.String(), err)
	}

	if !decompress || frameTable == nil || frameTable.CompressionType == CompressionNone {
		return fetchRange, respBody, nil
	}

	switch frameTable.CompressionType {
	case CompressionZstd:
		// TODO LEV get a recycled decoder from a pool?
		dec, err := zstd.NewReader(respBody)
		if err != nil {
			return Range{}, nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		// zstdCloser provides an io.Closer compliant Close() that will returns
		// the decoder to the pool.
		return fetchRange, &zstdCloser{Decoder: dec}, nil

	default:
		return Range{}, nil, fmt.Errorf("unsupported compression type: %s", frameTable.CompressionType)
	}
}

type zstdCloser struct {
	*zstd.Decoder
}

func (c *zstdCloser) Close() error {
	// return to the pool, see ^^
	c.Decoder.Close()
	return nil
}

func (s *Storage) GetBlob(ctx context.Context, path string, userBuffer []byte) ([]byte, error) {
	r, err := s.Provider.KV.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("getting blob from storage: %w", err)
	}

	receiveBuf := bytes.NewBuffer(userBuffer)
	n, err := receiveBuf.ReadFrom(r)
	if err != nil {
		return nil, fmt.Errorf("reading blob from storage reader: %w", err)
	}
	if n > int64(len(userBuffer)) {
		return nil, fmt.Errorf("user buffer too small: read %d bytes, buffer size %d", n, len(userBuffer))
	}

	return receiveBuf.Bytes(), nil
}

func (s *Storage) Exists(ctx context.Context, path string) (bool, error) {
	_, err := s.Provider.KV.Size(ctx, path)

	return err == nil, ignoreNotExists(err)
}

func uploadFileInParallel(ctx context.Context, asPath string, in io.ReaderAt, size int64, uploader MultipartUploader, partSize, maxConcurrency int) error {
	// Calculate number of parts
	numParts := int(math.Ceil(float64(size) / float64(partSize)))
	if numParts == 0 {
		numParts = 1 // Always upload at least 1 part, even for empty files
	}

	if err := uploader.Start(ctx); err != nil {
		return fmt.Errorf("failed to initiate multipart upload: %w", err)
	}

	// Initiate multipart upload
	eg, egCtx := errgroup.WithContext(ctx) // Create a separate context for the error group
	if maxConcurrency > 0 {
		eg.SetLimit(maxConcurrency) // Limit concurrent goroutines
	}

	// Upload each part concurrently
	for partNumber := 1; partNumber <= numParts; partNumber++ {
		// Read chunk from file
		offset := int64(partNumber-1) * int64(partSize)
		actualSize := partSize
		if offset+int64(partSize) > size {
			actualSize = int(size - offset)
		}
		part := make([]byte, actualSize)
		if _, err := in.ReadAt(part, offset); err != nil {
			return fmt.Errorf("failed to read chunk for part %d: %w", partNumber, err)
		}

		eg.Go(func() error {
			// Check if context was cancelled
			select {
			case <-egCtx.Done():
				return fmt.Errorf("part %d failed: %w", partNumber, egCtx.Err())
			default:
			}

			// Upload part
			err := uploader.UploadPart(egCtx, partNumber, part)
			if err != nil {
				return fmt.Errorf("failed to upload part %d: %w", partNumber, err)
			}

			return nil
		})
	}

	// Wait for all parts to complete or first error
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	if err := uploader.Complete(ctx); err != nil { // Use original ctx, not egCtx
		return fmt.Errorf("failed to complete upload: %w", err)
	}

	return nil
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
