package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
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
	googleOperationTimeout         = 5 * time.Second
	googleBufferSize               = 4 << 20 // 4 MiB
	googleInitialBackoff           = 10 * time.Millisecond
	googleMaxBackoff               = 10 * time.Second
	googleBackoffMultiplier        = 2
	googleMaxAttempts              = 10
	defaultGRPCConnectionPoolSize  = 4
	defaultGCSEnableDirectPath     = false
	gcloudDefaultUploadConcurrency = 16

	// Read retry configuration.
	//
	// We handle retries ourselves with a fresh context.WithTimeout per attempt
	// instead of relying on the GCS client library's built-in retry. The library
	// retries within the caller's context, so a single 10s timeout shared across
	// 10 retry attempts left later attempts with almost no time to complete.
	//
	// With this configuration each attempt gets a full googlePerAttemptTimeout
	// to succeed, and we apply exponential backoff between attempts.
	// Worst-case total time: 3 × 3s + ~0.6s backoff ≈ 10s per ReadAt call.
	//
	// These values are intentionally moderate because the UFFD page fault path
	// (the primary consumer) has its own retry loop around source.Slice() with
	// up to 4 total attempts. The combined worst-case is ~42s, which keeps
	// goroutine slot hold time bounded.
	googlePerAttemptTimeout    = 3 * time.Second
	googleMaxReadAttempts      = 3
	googleRetryInitialBackoff  = 200 * time.Millisecond
	googleRetryMaxBackoff      = 2 * time.Second
	googleRetryBackoffMultiply = 2

	gcsOperationAttr                           = "operation"
	gcsOperationAttrReadAt                     = "ReadAt"
	gcsOperationAttrWrite                      = "Write"
	gcsOperationAttrWriteFromFileSystem        = "WriteFromFileSystem"
	gcsOperationAttrWriteFromFileSystemOneShot = "WriteFromFileSystemOneShot"
	gcsOperationAttrWriteTo                    = "WriteTo"
	gcsOperationAttrSize                       = "Size"
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
	storage      *gcpStorage
	path         string
	handle       *storage.ObjectHandle // default handle with library retries for most operations
	readAtHandle *storage.ObjectHandle // handle with retries disabled — ReadAt uses retryWithBackoff instead

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
		option.WithGRPCDialOption(grpc.WithStatsHandler(otelgrpc.NewClientHandler())),
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

// newObject creates a gcpObject with both the default (library-retried) handle
// and the ReadAt handle (library retries disabled, retryWithBackoff instead).
func (s *gcpStorage) newObject(path string) *gcpObject {
	obj := s.bucket.Object(path)

	// Default handle with library-level retries for Size, OpenRangeReader, WriteTo, etc.
	handle := obj.Retryer(
		storage.WithMaxAttempts(googleMaxAttempts),
		storage.WithPolicy(storage.RetryAlways),
		storage.WithBackoff(gax.Backoff{
			Initial:    googleInitialBackoff,
			Max:        googleMaxBackoff,
			Multiplier: googleBackoffMultiplier,
		}),
	)

	// ReadAt handle with library retries disabled — retryWithBackoff gives
	// each attempt a fresh context.WithTimeout instead.
	readAtHandle := obj.Retryer(
		storage.WithMaxAttempts(1),
	)

	return &gcpObject{
		storage:      s,
		path:         path,
		handle:       handle,
		readAtHandle: readAtHandle,
		limiter:      s.limiter,
	}
}

func (s *gcpStorage) OpenSeekable(_ context.Context, path string, _ SeekableObjectType) (Seekable, error) {
	return s.newObject(path), nil
}

func (s *gcpStorage) OpenBlob(_ context.Context, path string, _ ObjectType) (Blob, error) {
	return s.newObject(path), nil
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

	return attrs.Size, nil
}

// OpenRangeReader opens a streaming reader for the given byte range.
//
// The reader's lifetime is governed by the caller's context — no extra timeout
// is added here. Callers that need a deadline (e.g. StreamingChunker with its
// 60 s fetch timeout) should set one on ctx before calling.
//
// Previously a 10 s timeout was applied to both the open and all subsequent
// reads, which caused progressive reads of large chunks (4 MiB) to be
// cancelled mid-stream.
func (o *gcpObject) OpenRangeReader(ctx context.Context, off, length int64) (io.ReadCloser, error) {
	reader, err := o.handle.NewRangeReader(ctx, off, length)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS range reader for %q at %d+%d: %w", o.path, off, length, err)
	}

	return reader, nil
}

func (o *gcpObject) ReadAt(ctx context.Context, buff []byte, off int64) (n int, err error) {
	timer := googleReadTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrReadAt))

	n, err = retryWithBackoff(ctx, func() (int, error) {
		return o.readAtOnce(ctx, buff, off)
	})

	if ignoreEOF(err) != nil {
		timer.Failure(ctx, int64(n))
	} else {
		timer.Success(ctx, int64(n))
	}

	return n, err
}

// readAtOnce performs a single GCS read attempt with its own timeout context.
func (o *gcpObject) readAtOnce(ctx context.Context, buff []byte, off int64) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, googlePerAttemptTimeout)
	defer cancel()

	reader, err := o.readAtHandle.NewRangeReader(ctx, off, int64(len(buff)))
	if err != nil {
		return 0, fmt.Errorf("failed to create GCS reader for %q: %w", o.path, err)
	}

	defer reader.Close()

	n, err := io.ReadFull(reader, buff)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}

	if ignoreEOF(err) != nil {
		return n, fmt.Errorf("failed to read %q: %w", o.path, err)
	}

	return n, err // preserve io.EOF for callers (e.g. cache layer's isCompleteRead)
}

func (o *gcpObject) Put(ctx context.Context, data []byte) error {
	timer := googleWriteTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrWrite))

	w := o.handle.NewWriter(ctx)

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

// WriteTo downloads the full object into dst. The caller's context governs the
// entire operation — no additional timeout is added because object sizes vary
// widely and a fixed deadline would truncate large reads.
func (o *gcpObject) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	timer := googleReadTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrWriteTo))

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

func (o *gcpObject) StoreFile(ctx context.Context, path string) (e error) {
	ctx, span := tracer.Start(ctx, "write to gcp from file system")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	bucketName := o.storage.bucket.BucketName()
	objectName := o.path

	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to get file size: %w", err)
	}

	// If the file is too small, the overhead of writing in parallel isn't worth the effort.
	// Write it in one shot instead.
	if fileInfo.Size() < gcpMultipartUploadChunkSize {
		timer := googleWriteTimerFactory.Begin(
			attribute.String(gcsOperationAttr, gcsOperationAttrWriteFromFileSystemOneShot),
		)

		data, err := os.ReadFile(path)
		if err != nil {
			timer.Failure(ctx, 0)

			return fmt.Errorf("failed to read file: %w", err)
		}

		err = o.Put(ctx, data)
		if err != nil {
			timer.Failure(ctx, int64(len(data)))

			return fmt.Errorf("failed to write file (%d bytes): %w", len(data), err)
		}

		timer.Success(ctx, int64(len(data)))

		return nil
	}

	timer := googleWriteTimerFactory.Begin(
		attribute.String(gcsOperationAttr, gcsOperationAttrWriteFromFileSystem),
	)

	maxConcurrency := gcloudDefaultUploadConcurrency
	if o.limiter != nil {
		uploadLimiter := o.limiter.GCloudUploadLimiter()
		if uploadLimiter != nil {
			semaphoreErr := uploadLimiter.Acquire(ctx, 1)
			if semaphoreErr != nil {
				timer.Failure(ctx, 0)

				return fmt.Errorf("failed to acquire semaphore: %w", semaphoreErr)
			}
			defer uploadLimiter.Release(1)
		}

		maxConcurrency = o.limiter.GCloudMaxTasks(ctx)
	}

	uploader, err := NewMultipartUploaderWithRetryConfig(
		ctx,
		bucketName,
		objectName,
		DefaultRetryConfig(),
	)
	if err != nil {
		timer.Failure(ctx, 0)

		return fmt.Errorf("failed to create multipart uploader: %w", err)
	}

	start := time.Now()
	count, err := uploader.UploadFileInParallel(ctx, path, maxConcurrency)
	if err != nil {
		timer.Failure(ctx, count)

		return fmt.Errorf("failed to upload file in parallel: %w", err)
	}

	logger.L().Debug(ctx, "Uploaded file in parallel",
		zap.String("bucket", bucketName),
		zap.String("object", objectName),
		zap.String("path", path),
		zap.Int("max_concurrency", maxConcurrency),
		zap.Int64("file_size", fileInfo.Size()),
		zap.Int64("duration", time.Since(start).Milliseconds()),
	)

	timer.Success(ctx, count)

	return nil
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
