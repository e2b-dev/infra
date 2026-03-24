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

	gcsOperationAttr                           = "operation"
	gcsOperationAttrWrite                      = "Write"
	gcsOperationAttrWriteFromFileSystem        = "WriteFromFileSystem"
	gcsOperationAttrWriteFromFileSystemOneShot = "WriteFromFileSystemOneShot"
	gcsOperationAttrWriteTo                    = "WriteTo"
	gcsOperationAttrSize                       = "Size"
	gcsOperationAttrReadAt                     = "ReadAt"
	gcsOperationAttrGetFrame                   = "GetFrame"
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
	_ FramedFile = (*gcpObject)(nil)
	_ Blob       = (*gcpObject)(nil)
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

func (s *gcpStorage) OpenFramedFile(_ context.Context, path string) (FramedFile, error) {
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

func (s *gcpStorage) OpenBlob(_ context.Context, path string) (Blob, error) {
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

func (o *gcpObject) openRangeReader(ctx context.Context, off int64, length int) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)

	reader, err := o.handle.NewRangeReader(ctx, off, int64(length))
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

func (o *gcpObject) ReadAt(ctx context.Context, buff []byte, off int64) (n int, err error) {
	timer := googleReadTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrReadAt))

	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)
	defer cancel()

	// The file should not be gzip compressed
	reader, err := o.handle.NewRangeReader(ctx, off, int64(len(buff)))
	if err != nil {
		timer.Failure(ctx, int64(n))

		return 0, fmt.Errorf("failed to create GCS reader for %q: %w", o.path, err)
	}

	defer reader.Close()

	n, err = io.ReadFull(reader, buff)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}

	if ignoreEOF(err) != nil {
		timer.Failure(ctx, int64(n))

		return n, fmt.Errorf("failed to read %q: %w", o.path, err)
	}

	timer.Success(ctx, int64(n))

	return n, err
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

func (o *gcpObject) StoreFile(ctx context.Context, path string, cfg *CompressConfig) (_ *FrameTable, _ [32]byte, e error) {
	maxConcurrency := gcloudDefaultUploadConcurrency
	if o.limiter != nil {
		uploadLimiter := o.limiter.GCloudUploadLimiter()
		if uploadLimiter != nil {
			if err := uploadLimiter.Acquire(ctx, 1); err != nil {
				return nil, [32]byte{}, fmt.Errorf("failed to acquire upload semaphore: %w", err)
			}
			defer uploadLimiter.Release(1)
		}

		maxConcurrency = o.limiter.GCloudMaxTasks(ctx)
	}

	if cfg.IsEnabled() {
		return o.storeFileCompressed(ctx, path, cfg, maxConcurrency)
	}

	ctx, span := tracer.Start(ctx, "write to gcp from file system")
	defer func() {
		recordError(span, e)
		span.End()
	}()

	bucketName := o.storage.bucket.BucketName()
	objectName := o.path

	fileInfo, err := os.Stat(path)
	if err != nil {
		e = fmt.Errorf("failed to get file size: %w", err)

		return nil, [32]byte{}, e
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
			e = fmt.Errorf("failed to read file: %w", err)

			return nil, [32]byte{}, e
		}

		err = o.Put(ctx, data)
		if err != nil {
			timer.Failure(ctx, int64(len(data)))
			e = fmt.Errorf("failed to write file (%d bytes): %w", len(data), err)

			return nil, [32]byte{}, e
		}

		timer.Success(ctx, int64(len(data)))

		return nil, [32]byte{}, e
	}

	timer := googleWriteTimerFactory.Begin(
		attribute.String(gcsOperationAttr, gcsOperationAttrWriteFromFileSystem),
	)

	uploader, err := NewMultipartUploaderWithRetryConfig(
		ctx,
		bucketName,
		objectName,
		DefaultRetryConfig(),
		nil,
	)
	if err != nil {
		timer.Failure(ctx, 0)
		e = fmt.Errorf("failed to create multipart uploader: %w", err)

		return nil, [32]byte{}, e
	}

	start := time.Now()
	count, err := uploader.UploadFileInParallel(ctx, path, maxConcurrency)
	if err != nil {
		timer.Failure(ctx, count)
		e = fmt.Errorf("failed to upload file in parallel: %w", err)

		return nil, [32]byte{}, e
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

	return nil, [32]byte{}, e
}

func (o *gcpObject) storeFileCompressed(ctx context.Context, localPath string, cfg *CompressConfig, maxConcurrency int) (*FrameTable, [32]byte, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to open local file %s: %w", localPath, err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to stat local file %s: %w", localPath, err)
	}

	uploader, err := NewMultipartUploaderWithRetryConfig(
		ctx,
		o.storage.bucket.BucketName(),
		o.path,
		DefaultRetryConfig(),
		map[string]string{
			MetadataKeyUncompressedSize: strconv.FormatInt(fi.Size(), 10),
		},
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

func (o *gcpObject) GetFrame(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	timer := googleReadTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrGetFrame))

	r, err := ReadFrame(ctx, o.openRangeReader, "GCS:"+o.path, offsetU, frameTable, decompress, buf, readSize, onRead)
	if err != nil {
		timer.Failure(ctx, int64(r.Length))

		return r, err
	}

	timer.Success(ctx, int64(r.Length))

	return r, nil
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
