package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Each compressed frame contains 1+ chunks.
const (
	// defaultChunkSizeU is the uncompressed chunk size for compression.
	// Must be a multiple of MemoryChunkSize to ensure aligned block/prefetch
	// requests do not cross compression frame boundaries.
	defaultChunkSizeU             = MemoryChunkSize
	defaultTargetFrameSizeC       = 4 * megabyte      // target compressed frame size
	defaultZstdCompressionLevel   = zstd.SpeedDefault // default compression level for zstd encoder
	defaultCompressionConcurrency = 0                 // use default compression concurrency settings
	defaultUploadPartSize         = 50 * megabyte
)

type ChunkerType byte

const (
	UncompressedMMapChunker ChunkerType = iota
	DecompressMMapChunker
	CompressLRUChunker
	CompressMMapLRUChunker
)

// Global flags for compression behavior. These will become feature flags later.
var (
	// EnableGCSCompression controls whether files are compressed when uploading to GCS.
	// When false, files are uploaded uncompressed even if compression options are provided.
	EnableGCSCompression = true

	// EnableNFSCompressedCache controls whether the NFS cache stores compressed frames.
	// When true (default): Cache stores compressed frames, decompresses on read.
	// When false: Cache stores uncompressed chunks (inner decompresses, cache stores raw data).
	EnableNFSCompressedCache = false

	CompressedChunkerType   = CompressMMapLRUChunker
	UncompressedChunkerType = UncompressedMMapChunker
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

func (o *FrameOffset) String() string {
	return fmt.Sprintf("U:%#x/C:%#x", o.U, o.C)
}

type FrameSize struct {
	U int32
	C int32
}

func (s FrameSize) String() string {
	return fmt.Sprintf("U:%#x/C:%#x", s.U, s.C)
}

type Range struct {
	Start  int64
	Length int
}

func (r Range) String() string {
	return fmt.Sprintf("%#x/%#x", r.Start, r.Length)
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

	OnFrameReady func(offset FrameOffset, size FrameSize, data []byte) error
}

var DefaultCompressionOptions = &FramedUploadOptions{
	CompressionType:        CompressionZstd,
	ChunkSize:              defaultChunkSizeU,
	TargetFrameSize:        defaultTargetFrameSizeC,
	Level:                  int(defaultZstdCompressionLevel),
	CompressionConcurrency: defaultCompressionConcurrency,
	TargetPartSize:         defaultUploadPartSize,
}

// ValidateCompressionOptions checks that compression options are valid.
// ChunkSize must be a multiple of MemoryChunkSize to ensure alignment.
func ValidateCompressionOptions(opts *FramedUploadOptions) error {
	if opts == nil || opts.CompressionType == CompressionNone {
		return nil
	}
	chunkSize := opts.ChunkSize
	if chunkSize == 0 {
		chunkSize = defaultChunkSizeU
	}
	if chunkSize%MemoryChunkSize != 0 {
		return fmt.Errorf("compression ChunkSize (%d) must be a multiple of MemoryChunkSize (%d)", chunkSize, MemoryChunkSize)
	}

	return nil
}

type ReaderAt interface {
	ReadAt(ctx context.Context, p []byte, off int64) (n int, err error)
	Size(ctx context.Context) (int64, error)
}

// type Reader interface {
// 	Read(ctx context.Context, p []byte) (n int, err error)
// }

type FrameGetter interface {
	GetFrame(ctx context.Context, objectPath string, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte) (Range, error)
}

type FileStorer interface {
	StoreFile(ctx context.Context, inFilePath, asObjectPath string, opts *FramedUploadOptions) (ft *FrameTable, err error)
}

type Blobber interface {
	GetBlob(ctx context.Context, objectPath string) ([]byte, error)
	CopyBlob(ctx context.Context, objectPath string, dst io.Writer) (n int64, err error)
	StoreBlob(ctx context.Context, objectPath string, in io.Reader) error
}

type StorageProvider interface {
	FrameGetter
	FileStorer
	Blobber
	PublicUploader
	Manager
}

type Storage struct {
	*Backend
}

var _ StorageProvider = (*Storage)(nil)

func NewGCP(ctx context.Context, bucketName string, limiter *limit.Limiter) (*Storage, error) {
	backend, err := newGCPBackend(ctx, bucketName, limiter)
	if err != nil {
		return nil, err
	}

	return &Storage{
		Backend: backend,
	}, nil
}

func GetTemplateStorageProvider(ctx context.Context, limiter *limit.Limiter) (*Storage, error) {
	return getStorageForEnvironment(ctx, limiter, "LOCAL_TEMPLATE_STORAGE_BASE_PATH", "/tmp/templates", "TEMPLATE_BUCKET_NAME", "Bucket for storing template files")
}

func GetBuildCacheStorageProvider(ctx context.Context, limiter *limit.Limiter) (*Storage, error) {
	return getStorageForEnvironment(ctx, limiter, "LOCAL_BUILD_CACHE_STORAGE_BASE_PATH", "/tmp/build-cache", "BUILD_CACHE_BUCKET_NAME", "Bucket for storing build cache files")
}

// NewFileSystemStorage creates a Storage backed by local filesystem at basePath.
func NewFileSystemStorage(basePath string) *Storage {
	return &Storage{Backend: NewFS(basePath)}
}

func getStorageForEnvironment(ctx context.Context, limiter *limit.Limiter, localBaseEnv, defaultLocalBase, bucketEnv, bucketUsage string) (*Storage, error) {
	var provider *Backend
	var err error

	providerName := ProviderName(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))
	if providerName == LocalStorageProvider {
		basePath := env.GetEnv(localBaseEnv, defaultLocalBase)
		provider = NewFS(basePath)
	} else {
		bucketName := utils.RequiredEnv(bucketEnv, bucketUsage)
		provider, err = newCloudBackendForEnvironment(ctx, providerName, bucketName, limiter)
		if err != nil {
			return nil, err
		}
	}

	return &Storage{
		Backend: provider,
	}, nil
}

func newCloudBackendForEnvironment(ctx context.Context, providerName ProviderName, bucketName string, limiter *limit.Limiter) (*Backend, error) {
	switch providerName {
	// cloud bucket-based storage
	case AWSStorageProvider:
		return newAWSBackend(ctx, bucketName)
	case GCPStorageProvider:
		return newGCPBackend(ctx, bucketName, limiter)
	default:
		return nil, fmt.Errorf("unknown storage backend: %s", providerName)
	}
}

// StoreFile compresses the given file and uploads it using multipart upload. If
// the compression type is unset, the file is uploaded in its entirety.
//
// TODO LEV If we use fixed-size chunks, we can optimize by reading/compressing
// in parallel; we can also split the file and still use variable-sized frames.
func (s *Storage) StoreFile(ctx context.Context, inFilePath, objectPath string, opts *FramedUploadOptions) (ft *FrameTable, e error) {
	ctx, span := tracer.Start(ctx, "store file")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	if err := ValidateCompressionOptions(opts); err != nil {
		return nil, err
	}

	noCompression := !EnableGCSCompression || opts == nil || opts.CompressionType == CompressionNone
	partSize := defaultUploadPartSize
	if opts != nil && opts.TargetPartSize > 0 {
		partSize = opts.TargetPartSize
	}

	in, err := os.Open(inFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer utils.Cleanup(ctx, "failed to close file", in.Close)

	stat, err := in.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}

	sizeU := stat.Size()

	if noCompression && (s.Backend.MultipartUploaderFactory == nil || sizeU <= int64(partSize)) {
		// If not using multipart or compressed upload, fall through to simple put.
		_, err = s.Backend.Upload(ctx, objectPath, in)

		return nil, err
	}

	timer := googleWriteTimerFactory.Begin(
		attribute.String(gcsOperationAttr, gcsOperationAttrStore))

	// For compressed uploads, include the uncompressed size as metadata.
	var metadata map[string]string
	if !noCompression {
		metadata = map[string]string{
			MetadataKeyUncompressedSize: fmt.Sprintf("%d", sizeU),
		}
	}

	partUploader, cleanup, maxConcurrency, err := s.Backend.MakeMultipartUpload(ctx, objectPath, DefaultRetryConfig(), metadata)
	defer cleanup()
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, fmt.Errorf("failed to initiate upload: %w", err)
	}

	if noCompression {
		err = uploadFileInParallel(ctx, in, sizeU, partUploader, partSize, maxConcurrency)
	} else {
		ft, err = newFrameEncoder(opts, partUploader, int64(partSize), maxConcurrency).uploadFramed(ctx, in)
	}

	if err != nil {
		timer.Failure(ctx, 0)

		return nil, err
	}

	timer.Success(ctx, sizeU)

	return ft, nil
}

// GetFrame reads a single frame from storage into buf. The caller MUST provide
// a buffer sized for the full uncompressed frame (use frameTable.FrameFor to
// get the frame size). Returns the compressed range that was fetched.
// When frameTable is nil (uncompressed data), reads directly without frame translation.
func (s *Storage) GetFrame(ctx context.Context, objectPath string, offset int64, frameTable *FrameTable, decompress bool, buf []byte) (Range, error) {
	// Handle uncompressed data (nil frameTable) - read directly without frame translation
	if !IsCompressed(frameTable) {
		return s.getFrameUncompressed(ctx, objectPath, offset, buf)
	}

	// Get the frame info: translate U offset -> C offset for fetching
	frameStart, frameSize, err := frameTable.FrameFor(offset)
	if err != nil {
		return Range{}, fmt.Errorf("get frame for offset %#x, object %s: %w", offset, objectPath, err)
	}

	// Validate buffer size - caller must provide a buffer for the full frame
	expectedSize := int(frameSize.C)
	if decompress && IsCompressed(frameTable) {
		expectedSize = int(frameSize.U)
	}
	if len(buf) < expectedSize {
		return Range{}, fmt.Errorf("buffer too small: got %d bytes, need %d bytes for frame", len(buf), expectedSize)
	}

	// Fetch the compressed data from storage
	respBody, err := s.Backend.RangeGet(ctx, objectPath, frameStart.C, int(frameSize.C))
	if err != nil {
		return Range{}, fmt.Errorf("getting frame at %#x from %s in %s: %w", frameStart.C, objectPath, s.Backend.String(), err)
	}
	defer respBody.Close()

	var from io.Reader = respBody
	readSize := int(frameSize.C) // Default to compressed size

	if decompress && IsCompressed(frameTable) {
		// When decompressing, we read the uncompressed size from the decoder
		readSize = int(frameSize.U)

		switch frameTable.CompressionType {
		case CompressionZstd:
			// TODO LEV get a recycled decoder from a pool?
			dec, err := zstd.NewReader(respBody)
			if err != nil {
				return Range{}, fmt.Errorf("failed to create zstd decoder: %w", err)
			}
			defer dec.Close()
			from = dec

		default:
			return Range{}, fmt.Errorf("unsupported compression type: %s", frameTable.CompressionType)
		}
	}

	n, err := io.ReadFull(from, buf[:readSize])

	return Range{Start: frameStart.C, Length: n}, err
}

// getFrameUncompressed reads uncompressed data directly from storage without frame translation.
func (s *Storage) getFrameUncompressed(ctx context.Context, objectPath string, offset int64, buf []byte) (Range, error) {
	respBody, err := s.Backend.RangeGet(ctx, objectPath, offset, len(buf))
	if err != nil {
		return Range{}, fmt.Errorf("getting uncompressed data at %#x from %s in %s: %w", offset, objectPath, s.Backend.String(), err)
	}
	defer respBody.Close()

	n, err := io.ReadFull(respBody, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Range{}, fmt.Errorf("reading uncompressed data from %s: %w", objectPath, err)
	}

	return Range{Start: offset, Length: n}, nil
}

func (s *Storage) GetBlob(ctx context.Context, path string) ([]byte, error) {
	// TODO LEV metrics

	r, err := s.StartDownload(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("getting blob from storage: %w", err)
	}
	defer r.Close()

	return readAll(r)
}

func readAll(r io.Reader) ([]byte, error) {
	const initialBufferSize = 2 * megabyte
	buf := bytes.NewBuffer(make([]byte, 0, initialBufferSize))
	_, err := io.Copy(buf, r)

	return buf.Bytes(), err
}

func (s *Storage) CopyBlob(ctx context.Context, path string, dst io.Writer) (n int64, err error) {
	// TODO LEV metrics

	r, err := s.StartDownload(ctx, path)
	if err != nil {
		return 0, fmt.Errorf("getting blob from storage: %w", err)
	}
	defer r.Close()

	return io.Copy(dst, r)
}

func (s *Storage) StoreBlob(ctx context.Context, path string, in io.Reader) error {
	// TODO LEV metrics

	_, err := s.Upload(ctx, path, in)
	if err != nil {
		return fmt.Errorf("putting blob to storage: %w", err)
	}

	return nil
}

func Exists(ctx context.Context, s StorageProvider, path string) (bool, error) {
	_, _, err := s.Size(ctx, path)

	return err == nil, ignoreNotExists(err)
}

func uploadFileInParallel(ctx context.Context, in io.ReaderAt, size int64, uploader MultipartUploader, partSize, maxConcurrency int) error {
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

func StoreBlobFromFile(ctx context.Context, s StorageProvider, inFilePath, objectPath string) error {
	in, err := os.Open(inFilePath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer utils.Cleanup(ctx, "failed to close file", in.Close)

	err = s.StoreBlob(ctx, objectPath, in)
	if err != nil {
		return fmt.Errorf("failed to upload blob: %w", err)
	}

	return nil
}
