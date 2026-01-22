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
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	googleReadTimeout              = 10 * time.Second
	googleOperationTimeout         = 5 * time.Second
	googleBufferSize               = 2 << 21
	googleInitialBackoff           = 10 * time.Millisecond
	googleMaxBackoff               = 10 * time.Second
	googleBackoffMultiplier        = 2
	googleMaxAttempts              = 10
	gcloudDefaultUploadConcurrency = 16

	gcsOperationAttr                           = "operation"
	gcsOperationAttrReadAt                     = "ReadAt"
	gcsOperationAttrWrite                      = "Write"
	gcsOperationAttrWriteFromFileSystem        = "WriteFromFileSystem"
	gcsOperationAttrWriteFromFileSystemOneShot = "WriteFromFileSystemOneShot"
	gcsOperationAttrWriteTo                    = "WriteTo"
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
	_ Seekable = (*gcpObject)(nil)
	_ Blob     = (*gcpObject)(nil)
)

func NewGCP(ctx context.Context, bucketName string, limiter *limit.Limiter) (StorageProvider, error) {
	client, err := storage.NewGRPCClient(ctx,
		option.WithGRPCConnectionPool(4),
		option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(32*megabyte)),
		option.WithGRPCDialOption(grpc.WithInitialWindowSize(4*megabyte)),
	)
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
	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	attrs, err := o.handle.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			// use ours instead of theirs
			return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", o.path, ErrObjectNotExist)
		}

		return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", o.path, err)
	}

	return attrs.Size, nil
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

	for reader.Remain() > 0 {
		nr, err := reader.Read(buff[n:])
		n += nr

		if err == nil {
			continue
		}

		if errors.Is(err, io.EOF) {
			break
		}

		timer.Failure(ctx, int64(n))

		return n, fmt.Errorf("failed to read %q: %w", o.path, err)
	}

	timer.Success(ctx, int64(n))

	return n, nil
}

func (o *gcpObject) Put(ctx context.Context, data []byte) (e error) {
	timer := googleWriteTimerFactory.Begin(attribute.String(gcsOperationAttr, gcsOperationAttrWrite))

	w := o.handle.NewWriter(ctx)
	defer func() {
		if err := w.Close(); err != nil {
			e = errors.Join(e, fmt.Errorf("failed to write to %q: %w", o.path, err))
		}
	}()

	c, err := io.Copy(w, bytes.NewReader(data))
	if err != nil && !errors.Is(err, io.EOF) {
		timer.Failure(ctx, c)

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
