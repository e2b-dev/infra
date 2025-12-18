package storage

import (
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

type gcpBucketStore struct {
	client *storage.Client
	bucket *storage.BucketHandle

	limiter *limit.Limiter
}

var _ StorageProvider = (*gcpBucketStore)(nil)

type gcpObj struct {
	store   *gcpBucketStore
	path    string
	handle  *storage.ObjectHandle
	limiter *limit.Limiter
}

type gcpObject struct {
	gcpObj
}

type gcpFramedWriter struct {
	gcpObj

	opts *CompressionOptions
}

type gcpFramedReader struct {
	gcpObj

	compressedInfo *CompressedInfo
}

var (
	_ ObjectProvider = (*gcpObject)(nil)
	_ FramedWriter   = (*gcpFramedWriter)(nil)
	_ FramedReader   = (*gcpFramedReader)(nil)
)

func NewGCPBucketStorageProvider(ctx context.Context, bucketName string, limiter *limit.Limiter) (StorageProvider, error) {
	return newGCPBucketStore(ctx, bucketName, limiter)
}

func newGCPBucketStore(ctx context.Context, bucketName string, limiter *limit.Limiter) (*gcpBucketStore, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &gcpBucketStore{
		client:  client,
		bucket:  client.Bucket(bucketName),
		limiter: limiter,
	}, nil
}

func (g *gcpBucketStore) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	objects := g.bucket.Objects(ctx, &storage.Query{Prefix: prefix + "/"})

	for {
		object, err := objects.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			return fmt.Errorf("error when iterating over template objects: %w", err)
		}

		err = g.bucket.Object(object.Name).Delete(ctx)
		if err != nil {
			return fmt.Errorf("error when deleting template object: %w", err)
		}
	}

	return nil
}

func (g *gcpBucketStore) GetDetails() string {
	return fmt.Sprintf("[GCP Storage, bucket set to %s]", g.bucket.BucketName())
}

func (g *gcpBucketStore) UploadSignedURL(_ context.Context, path string, ttl time.Duration) (string, error) {
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

	url, err := storage.SignedURL(g.bucket.BucketName(), path, opts)
	if err != nil {
		return "", fmt.Errorf("failed to create signed URL for GCS object (%s): %w", path, err)
	}

	return url, nil
}

func (g *gcpBucketStore) handle(path string) *storage.ObjectHandle {
	return g.bucket.Object(path).Retryer(
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
}

func (g *gcpBucketStore) OpenFramedWriter(_ context.Context, path string, opts *CompressionOptions) (FramedWriter, error) {
	return &gcpFramedWriter{
		gcpObj: gcpObj{
			store:   g,
			path:    path,
			handle:  g.handle(path),
			limiter: g.limiter,
		},
		opts: opts,
	}, nil
}

func (g *gcpBucketStore) OpenFramedReader(_ context.Context, path string, frameInfo *CompressedInfo) (FramedReader, error) {
	return &gcpFramedReader{
		gcpObj: gcpObj{
			store:   g,
			path:    path,
			handle:  g.handle(path),
			limiter: g.limiter,
		},
		compressedInfo: frameInfo,
	}, nil
}

func (g *gcpBucketStore) OpenObject(_ context.Context, path string, _ ObjectType) (ObjectProvider, error) {
	return &gcpObject{
		gcpObj: gcpObj{
			store:   g,
			path:    path,
			handle:  g.handle(path),
			limiter: g.limiter,
		},
	}, nil
}

func (g *gcpObject) Delete(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	if err := g.handle.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete %q: %w", g.path, err)
	}

	return nil
}

func (g *gcpObject) Exists(ctx context.Context) (bool, error) {
	_, err := g.Size(ctx)

	return err == nil, ignoreNotExists(err)
}

func (g *gcpObject) Size(ctx context.Context) (int64, error) {
	return g.size(ctx)
}

func (g *gcpObject) size(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	attrs, err := g.handle.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			// use ours instead of theirs
			return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", g.path, ErrObjectNotExist)
		}

		return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", g.path, err)
	}

	return attrs.Size, nil
}

func (g *gcpObj) ReadAt(ctx context.Context, buff []byte, off int64) (n int, err error) {
	timer := googleReadTimerFactory.Begin()

	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)
	defer cancel()

	// The file should not be gzip compressed
	reader, err := g.handle.NewRangeReader(ctx, off, int64(len(buff)))
	if err != nil {
		return 0, fmt.Errorf("failed to create GCS reader for %q: %w", g.path, err)
	}

	defer reader.Close()

	for reader.Remain() > 0 {
		nr, readErr := reader.Read(buff[n:])
		n += nr

		if readErr == nil {
			continue
		}

		if errors.Is(readErr, io.EOF) {
			break
		}

		return n, fmt.Errorf("failed to read %q: %w", g.path, readErr)
	}

	timer.End(ctx, int64(n))

	return n, nil
}

func (g *gcpObj) Write(ctx context.Context, data []byte) (n int, e error) {
	timer := googleWriteTimerFactory.Begin()
	defer func() {
		if e == nil {
			timer.End(ctx, int64(n))
		}
	}()

	w := g.handle.NewWriter(ctx)
	defer func() {
		if err := w.Close(); err != nil {
			e = errors.Join(e, fmt.Errorf("failed to write to %q: %w", g.path, err))
		}
	}()

	n, err := w.Write(data)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("failed to write to %q: %w", g.path, err)
	}

	return n, nil
}

func (g *gcpObject) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	timer := googleReadTimerFactory.Begin()

	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)
	defer cancel()

	reader, err := g.handle.NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return 0, fmt.Errorf("failed to create reader for %q: %w", g.path, ErrObjectNotExist)
		}

		return 0, fmt.Errorf("failed to create reader for %q: %w", g.path, err)
	}

	defer reader.Close()

	buff := make([]byte, googleBufferSize)
	n, err := io.CopyBuffer(dst, reader, buff)
	if err != nil {
		return n, fmt.Errorf("failed to copy %q to buffer: %w", g.path, err)
	}

	timer.End(ctx, n)

	return n, nil
}

func (g *gcpObj) CopyFromFileSystem(ctx context.Context, path string) error {
	timer := googleWriteTimerFactory.Begin()

	bucketName := g.store.bucket.BucketName()
	objectName := g.path
	filePath := path

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file size: %w", err)
	}

	// If the file is too small, the overhead of writing in parallel isn't worth the effort.
	// Write it in one shot instead.
	if fileInfo.Size() < gcpMultipartUploadPartSize {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		if _, err = g.Write(ctx, data); err != nil {
			return fmt.Errorf("failed to write file (%d bytes): %w", len(data), err)
		}

		timer.End(ctx, int64(len(data)), attribute.String("method", "one-shot"))

		return nil
	}

	maxConcurrency := gcloudDefaultUploadConcurrency
	if g.limiter != nil {
		uploadLimiter := g.limiter.GCloudUploadLimiter()
		if uploadLimiter != nil {
			semaphoreErr := uploadLimiter.Acquire(ctx, 1)
			if semaphoreErr != nil {
				return fmt.Errorf("failed to acquire semaphore: %w", semaphoreErr)
			}
			defer uploadLimiter.Release(1)
		}

		maxConcurrency = g.limiter.GCloudMaxTasks(ctx)
	}

	uploader, err := newGCPUploaderWithRetryConfig(
		ctx,
		bucketName,
		objectName,
		DefaultRetryConfig(),
	)
	if err != nil {
		return fmt.Errorf("failed to create multipart uploader: %w", err)
	}

	start := time.Now()
	err = MultipartUploadFile(ctx, filePath, uploader, maxConcurrency)
	if err != nil {
		return fmt.Errorf("failed to upload file in parallel: %w", err)
	}

	logger.L().Debug(ctx, "Uploaded file in parallel",
		zap.String("bucket", bucketName),
		zap.String("object", objectName),
		zap.String("path", filePath),
		zap.Int("max_concurrency", maxConcurrency),
		zap.Int64("file_size", fileInfo.Size()),
		zap.Int64("duration", time.Since(start).Milliseconds()),
	)

	timer.End(ctx, fileInfo.Size(), attribute.String("method", "multipart"))

	return nil
}

func (g *gcpFramedWriter) StoreFromFileSystem(ctx context.Context, path string) (*CompressedInfo, error) {
	if g.opts == nil || g.opts.CompressionType == CompressionNone {
		return nil, g.gcpObj.CopyFromFileSystem(ctx, path)
	}

	timer := googleWriteTimerFactory.Begin()

	bucketName := g.store.bucket.BucketName()
	objectName := g.path
	filePath := path

	maxConcurrency := gcloudDefaultUploadConcurrency
	if g.limiter != nil {
		uploadLimiter := g.limiter.GCloudUploadLimiter()
		if uploadLimiter != nil {
			semaphoreErr := uploadLimiter.Acquire(ctx, 1)
			if semaphoreErr != nil {
				return nil, fmt.Errorf("failed to acquire semaphore: %w", semaphoreErr)
			}
			defer uploadLimiter.Release(1)
		}

		maxConcurrency = g.limiter.GCloudMaxTasks(ctx)
	}

	uploader, err := newGCPUploaderWithRetryConfig(
		ctx,
		bucketName,
		objectName,
		DefaultRetryConfig(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create multipart uploader: %w", err)
	}

	start := time.Now()
	info, err := MultipartCompressUploadFile(ctx, filePath, uploader, maxConcurrency, g.opts)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file in parallel: %w", err)
	}

	logger.L().Debug(ctx, "Uploaded file in parallel",
		zap.String("bucket", bucketName),
		zap.String("object", objectName),
		zap.String("path", filePath),
		zap.Int("max_concurrency", maxConcurrency),
		zap.Int64("compressed_size", info.TotalCompressedSize()),
		zap.Int64("original_size", info.TotalUncompressedSize()),
		zap.Int64("duration", time.Since(start).Milliseconds()),
	)

	timer.End(ctx, info.TotalUncompressedSize(), attribute.String("method", "multipart"))

	return info, nil
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

func (g *gcpFramedReader) NewRangeReader(ctx context.Context, offset int64, length int64) (io.ReadCloser, error) {
	return g.handle.NewRangeReader(ctx, offset, length)
}

func (g *gcpFramedReader) ReadAt(ctx context.Context, userBuf []byte, offset int64) (n int, err error) {
	if g.compressedInfo == nil || g.compressedInfo.CompressionType == CompressionNone {
		return g.gcpObj.ReadAt(ctx, userBuf, offset)
	}
	if offset < g.compressedInfo.FramesStartAt.U {
		return 0, fmt.Errorf("offset %d is before start of available framed data %d", offset, g.compressedInfo.FramesStartAt.U)
	}

	timer := googleReadTimerFactory.Begin()

	g.compressedInfo.DownloadSlice(ctx, g, userBuf, offset)

	timer.End(ctx, int64(len(userBuf)), attribute.String("method", "frame"))

	return len(userBuf), nil
}

func (g *gcpFramedReader) Size(_ context.Context) (int64, error) {
	if g.compressedInfo == nil {
		return 0, fmt.Errorf("TODO! implement for missing compression info")
	}

	return g.compressedInfo.TotalUncompressedSize(), nil
}
