package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	googleReadTimeout              = 10 * time.Second
	googleOperationTimeout         = 5 * time.Second
	googleBufferSize               = 4 << 20 // 4 MiB
	googleInitialBackoff           = 10 * time.Millisecond
	googleMaxBackoff               = 10 * time.Second
	googleBackoffMultiplier        = 2
	googleMaxAttempts              = 10
	defaultGRPCConnectionPoolSize  = 4
	defaultGCSEnableDirectPath     = false
	gcloudDefaultUploadConcurrency = 16

	gcsOperationAttr                    = "operation"
	gcsOperationAttrWrite               = "Write"
	gcsOperationAttrWriteFromFileSystem = "WriteFromFileSystem"
	gcsOperationAttrWriteTo             = "WriteTo"
	gcsOperationAttrSize                = "Size"
	// gcsOperationAttrReadAt tags GCS read timer metrics for OpenRangeReader
	// (the method was renamed from ReadAt; value kept for dashboard compatibility).
	gcsOperationAttrReadAt = "ReadAt"
)

var (
	googleReadTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.gcs.read",
		"Duration of GCS reads",
		"Total GCS bytes read",
		"Total GCS reads",
	))
	googleWriteTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.gcs.write",
		"Duration of GCS writes",
		"Total bytes written to GCS",
		"Total writes to GCS",
	))
)

type gcpStorage struct {
	client *storage.Client
	bucket *storage.BucketHandle

	limiter *limit.Limiter
}

var _ StorageProvider = (*gcpStorage)(nil)

type gcpObject struct {
	storage *gcpStorage
	path    string
	handle  *storage.ObjectHandle

	limiter *limit.Limiter
}

var (
	_ Seekable        = (*gcpObject)(nil)
	_ Blob            = (*gcpObject)(nil)
	_ StreamingReader = (*gcpObject)(nil)
)

func NewGCP(ctx context.Context, bucketName string, limiter *limit.Limiter) (StorageProvider, error) {
	grpcPoolSize, err := env.GetEnvAsInt("GCS_GRPC_CONNECTION_POOL_SIZE", defaultGRPCConnectionPoolSize)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GCS_GRPC_CONNECTION_POOL_SIZE: %w", err)
	}

	opts := []option.ClientOption{
		option.WithGRPCConnectionPool(grpcPoolSize),
		option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(32 * megabyte)),
		option.WithGRPCDialOption(grpc.WithInitialWindowSize(4 * megabyte)),
		internaloption.EnableDirectPath(defaultGCSEnableDirectPath),
	}

	client, err := storage.NewGRPCClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &gcpStorage{
		client:  client,
		bucket:  client.Bucket(bucketName),
		limiter: limiter,
	}, nil
}

func (s *gcpStorage) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	objects := s.bucket.Objects(ctx, &storage.Query{Prefix: prefix + "/"})

	for {
		object, err := objects.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			return fmt.Errorf("error when iterating over template objects: %w", err)
		}

		err = s.bucket.Object(object.Name).Delete(ctx)
		if err != nil {
			return fmt.Errorf("error when deleting template object: %w", err)
		}
	}

	return nil
}

func (s *gcpStorage) GetDetails() string {
	return fmt.Sprintf("[GCP Storage, bucket set to %s]", s.bucket.BucketName())
}

func (s *gcpStorage) UploadSignedURL(_ context.Context, path string, ttl time.Duration) (string, error) {
	token, err := parseServiceAccountBase64(consts.GoogleServiceAccountSecret)
	if err != nil {
		return "", fmt.Errorf("failed to parse GCP service account: %w", err)
	}

	opts := &storage.SignedURLOptions{
		GoogleAccessID: token.ClientEmail,
		PrivateKey:     []byte(token.PrivateKey),
		Method:         http.MethodPut,
		Expires:        time.Now().Add(ttl),
	}

	url, err := storage.SignedURL(s.bucket.BucketName(), path, opts)
	if err != nil {
		return "", fmt.Errorf("failed to create signed URL for GCS object (%s): %w", path, err)
	}

	return url, nil
}

func (s *gcpStorage) OpenSeekable(_ context.Context, path string, _ SeekableObjectType) (Seekable, error) {
	handle := s.bucket.Object(path).Retryer(
		storage.WithMaxAttempts(googleMaxAttempts),
		storage.WithPolicy(storage.RetryAlways),
		storage.WithBackoff(
			gax.Backoff{
				Initial:    googleInitialBackoff,
				Max:        googleMaxBackoff,
				Multiplier: googleBackoffMultiplier,
			},
		),
	)

	return &gcpObject{
		storage: s,
		path:    path,
		handle:  handle,

		limiter: s.limiter,
	}, nil
}

func (s *gcpStorage) OpenBlob(_ context.Context, path string, _ ObjectType) (Blob, error) {
	handle := s.bucket.Object(path).Retryer(
		storage.WithMaxAttempts(googleMaxAttempts),
		storage.WithPolicy(storage.RetryAlways),
		storage.WithBackoff(
			gax.Backoff{
				Initial:    googleInitialBackoff,
				Max:        googleMaxBackoff,
				Multiplier: googleBackoffMultiplier,
			},
		),
	)

	return &gcpObject{
		storage: s,
		path:    path,
		handle:  handle,

		limiter: s.limiter,
	}, nil
}

func (o *gcpObject) Delete(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	if err := o.handle.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete %q: %w", o.path, err)
	}

	return nil
}

func (o *gcpObject) Exists(ctx context.Context) (bool, error) {
	_, err := o.Size(ctx)

	return err == nil, ignoreNotExists(err)
}

func (o *gcpObject) Size(ctx context.Context) (int64, error) {
	timer := googleReadTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrSize))

	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	attrs, err := o.handle.Attrs(ctx)
	if err != nil {
		timer.Failure(ctx, 0)

		if errors.Is(err, storage.ErrObjectNotExist) {
			// use ours instead of theirs
			return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", o.path, ErrObjectNotExist)
		}

		return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", o.path, err)
	}

	timer.Success(ctx, 0)

	if v, ok := attrs.Metadata[MetadataKeyUncompressedSize]; ok {
		parsed, parseErr := strconv.ParseInt(v, 10, 64)
		if parseErr == nil {
			return parsed, nil
		}
	}

	return attrs.Size, nil
}

func (o *gcpObject) openRangeReader(ctx context.Context, off, length int64) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)

	reader, err := o.handle.NewRangeReader(ctx, off, length)
	if err != nil {
		cancel()

		return nil, fmt.Errorf("failed to create GCS range reader for %q at %d+%d: %w", o.path, off, length, err)
	}

	return &cancelOnCloseReader{ReadCloser: reader, cancel: cancel}, nil
}

// cancelOnCloseReader wraps a ReadCloser and calls a CancelFunc on Close,
// ensuring the context used to create the reader is cleaned up.
type cancelOnCloseReader struct {
	io.ReadCloser

	cancel context.CancelFunc
}

func (r *cancelOnCloseReader) Close() error {
	defer r.cancel()

	return r.ReadCloser.Close()
}

func (o *gcpObject) Put(ctx context.Context, data []byte, opts ...PutOption) error {
	timer := googleWriteTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrWrite))

	w := o.handle.NewWriter(ctx)
	if putOpts := ApplyPutOptions(opts); len(putOpts.Metadata) > 0 {
		w.Metadata = putOpts.Metadata
	}

	c, err := io.Copy(w, bytes.NewReader(data))
	if err != nil && !errors.Is(err, io.EOF) {
		closeErr := w.Close()
		if closeErr != nil {
			logger.L().Warn(ctx, "failed to close GCS writer after copy error",
				zap.String("object", o.path),
				zap.NamedError("error_copy", err),
				zap.Error(closeErr),
			)
		}

		timer.Failure(ctx, c)

		// ResourceExhausted from GCS means per-object mutation rate limiting —
		// multiple concurrent writers racing to write the same content-addressed object.
		if isResourceExhausted(err) {
			return ErrObjectRateLimited
		}

		return fmt.Errorf("failed to write to %q: %w", o.path, err)
	}

	// For small objects the GCS Writer buffers data in memory during Write()
	// and performs the actual upload during Close(). ResourceExhausted errors
	// from per-object mutation rate limiting will surface here.
	if err := w.Close(); err != nil {
		timer.Failure(ctx, c)

		if isResourceExhausted(err) {
			return ErrObjectRateLimited
		}

		return fmt.Errorf("failed to write to %q: %w", o.path, err)
	}

	timer.Success(ctx, c)

	return nil
}

func (o *gcpObject) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	timer := googleReadTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrWriteTo))

	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)
	defer cancel()

	reader, err := o.handle.NewReader(ctx)
	if err != nil {
		timer.Failure(ctx, 0)

		if errors.Is(err, storage.ErrObjectNotExist) {
			return 0, fmt.Errorf("failed to create reader for %q: %w", o.path, ErrObjectNotExist)
		}

		return 0, fmt.Errorf("failed to create reader for %q: %w", o.path, err)
	}

	defer reader.Close()

	buff := make([]byte, googleBufferSize)
	n, err := io.CopyBuffer(dst, reader, buff)
	if err != nil {
		timer.Failure(ctx, n)

		return n, fmt.Errorf("failed to copy %q to buffer: %w", o.path, err)
	}

	timer.Success(ctx, n)

	return n, nil
}

func (o *gcpObject) StoreFile(ctx context.Context, path string, opts ...PutOption) (_ *FrameTable, _ [32]byte, e error) {
	ctx, span := tracer.Start(ctx, "write to gcp from file system")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	putOpts := ApplyPutOptions(opts)

	bucketName := o.storage.bucket.BucketName()
	objectName := o.path

	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to get file size: %w", err)
	}

	cfg := CompressConfigFromOpts(putOpts)

	// Tag the upload timer with the compression mode so dashboards can split
	// duration/throughput by codec and level. Type is "none" when disabled.
	timer := googleWriteTimerFactory.Begin(
		attribute.String(gcsOperationAttr, gcsOperationAttrWriteFromFileSystem),
		attribute.String("compression.type", cfg.CompressionType().String()),
		attribute.Int("compression.level", cfg.Level),
	)

	maxConcurrency := gcloudDefaultUploadConcurrency
	if o.limiter != nil {
		uploadLimiter := o.limiter.GCloudUploadLimiter()
		if uploadLimiter != nil {
			semaphoreErr := uploadLimiter.Acquire(ctx, 1)
			if semaphoreErr != nil {
				timer.Failure(ctx, 0)

				return nil, [32]byte{}, fmt.Errorf("failed to acquire semaphore: %w", semaphoreErr)
			}
			defer uploadLimiter.Release(1)
		}

		maxConcurrency = o.limiter.GCloudMaxTasks(ctx)
	}

	// Compressed uploads always go through the multipart compressed path,
	// regardless of file size.
	if cfg.IsCompressionEnabled() {
		start := time.Now()
		ft, checksum, err := o.storeFileCompressed(ctx, path, cfg, maxConcurrency, putOpts)
		if err != nil {
			timer.Failure(ctx, fileInfo.Size())
		} else {
			timer.Success(ctx, fileInfo.Size())

			logger.L().Debug(ctx, "Uploaded file to GCS",
				zap.String("bucket", bucketName),
				zap.String("object", objectName),
				zap.String("source", path),
				zap.Int64("size_uncompressed", fileInfo.Size()),
				zap.Int64("size_compressed", ft.CompressedSize()),
				zap.String("compression", cfg.CompressionType().String()),
				zap.Int("frames", ft.NumFrames()),
				zap.Int64("duration_ms", time.Since(start).Milliseconds()),
			)
		}

		return ft, checksum, err
	}

	// If the file is too small, the overhead of writing in parallel isn't worth the effort.
	// Write it in one shot instead.
	if fileInfo.Size() < gcpMultipartUploadChunkSize {
		data, err := os.ReadFile(path)
		if err != nil {
			timer.Failure(ctx, 0)

			return nil, [32]byte{}, fmt.Errorf("failed to read file: %w", err)
		}

		err = o.Put(ctx, data, opts...)
		if err != nil {
			timer.Failure(ctx, int64(len(data)))

			return nil, [32]byte{}, fmt.Errorf("failed to write file (%d bytes): %w", len(data), err)
		}

		timer.Success(ctx, int64(len(data)))

		logger.L().Debug(ctx, "Uploaded file to GCS",
			zap.String("bucket", bucketName),
			zap.String("object", objectName),
			zap.String("source", path),
			zap.Int64("size_uncompressed", int64(len(data))),
			zap.String("compression", "none"),
		)

		return nil, [32]byte{}, e
	}

	uploader, err := NewMultipartUploaderWithRetryConfig(
		ctx,
		bucketName,
		objectName,
		DefaultRetryConfig(),
		putOpts.Metadata,
	)
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, [32]byte{}, fmt.Errorf("failed to create multipart uploader: %w", err)
	}

	start := time.Now()
	count, err := uploader.UploadFileInParallel(ctx, path, maxConcurrency)
	if err != nil {
		timer.Failure(ctx, count)

		return nil, [32]byte{}, fmt.Errorf("failed to upload file in parallel: %w", err)
	}

	logger.L().Debug(ctx, "Uploaded file to GCS",
		zap.String("bucket", bucketName),
		zap.String("object", objectName),
		zap.String("source", path),
		zap.Int64("size_uncompressed", fileInfo.Size()),
		zap.String("compression", "none"),
		zap.Int("max_concurrency", maxConcurrency),
		zap.Int64("duration_ms", time.Since(start).Milliseconds()),
	)

	timer.Success(ctx, count)

	return nil, [32]byte{}, e
}

func (o *gcpObject) storeFileCompressed(ctx context.Context, localPath string, cfg CompressConfig, maxConcurrency int, putOpts PutOptions) (*FrameTable, [32]byte, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to open local file %s: %w", localPath, err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to stat local file %s: %w", localPath, err)
	}

	// Merge caller metadata (e.g. team_id) with our internal uncompressed-size
	// bookkeeping. Internal key wins on collision.
	metadata := make(map[string]string, len(putOpts.Metadata)+1)
	maps.Copy(metadata, putOpts.Metadata)
	metadata[MetadataKeyUncompressedSize] = strconv.FormatInt(fi.Size(), 10)

	uploader, err := NewMultipartUploaderWithRetryConfig(
		ctx,
		o.storage.bucket.BucketName(),
		o.path,
		DefaultRetryConfig(),
		metadata,
	)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to create multipart uploader: %w", err)
	}

	return compressStream(ctx, file, cfg, uploader, maxConcurrency)
}

type gcpServiceToken struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

func parseServiceAccountBase64(serviceAccount string) (*gcpServiceToken, error) {
	decoded, err := base64.StdEncoding.DecodeString(serviceAccount)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	var sa gcpServiceToken
	if err := json.Unmarshal(decoded, &sa); err != nil {
		return nil, fmt.Errorf("failed to parse service account JSON: %w", err)
	}

	return &sa, nil
}

func (o *gcpObject) OpenRangeReader(ctx context.Context, offsetU int64, length int64, frameTable *FrameTable) (io.ReadCloser, error) {
	timer := googleReadTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrReadAt))

	if !frameTable.IsCompressed() {
		rc, err := o.openRangeReader(ctx, offsetU, length)
		if err != nil {
			timer.Failure(ctx, 0)

			return nil, err
		}

		return &timedReadCloser{inner: rc, timer: timer, ctx: ctx}, nil
	}

	r, err := frameTable.LocateCompressed(offsetU)
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, fmt.Errorf("get frame for offset %d, GCS:%s: %w", offsetU, o.path, err)
	}

	raw, err := o.openRangeReader(ctx, r.Offset, int64(r.Length))
	if err != nil {
		timer.Failure(ctx, 0)

		return nil, err
	}

	decompressed, err := newDecompressingReadCloser(raw, frameTable.CompressionType())
	if err != nil {
		raw.Close()
		timer.Failure(ctx, 0)

		return nil, err
	}

	return &timedReadCloser{inner: decompressed, timer: timer, ctx: ctx}, nil
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

func isResourceExhausted(err error) bool {
	type grpcStatusProvider interface {
		GRPCStatus() *status.Status
	}

	var se grpcStatusProvider
	if errors.As(err, &se) {
		return se.GRPCStatus().Code() == codes.ResourceExhausted
	}

	return false
}
