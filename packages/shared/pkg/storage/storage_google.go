package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"
)

const (
	googleReadTimeout       = 10 * time.Second
	googleOperationTimeout  = 5 * time.Second
	googleBufferSize        = 2 << 21
	googleInitialBackoff    = 10 * time.Millisecond
	googleMaxBackoff        = 10 * time.Second
	googleBackoffMultiplier = 2
	googleMaxAttempts       = 10
)

type GCPBucketStorageProvider struct {
	bucket        *storage.BucketHandle
	proxiedBucket *storage.BucketHandle
}

type GCPBucketStorageObjectProvider struct {
	storage       *GCPBucketStorageProvider
	path          string
	handle        *storage.ObjectHandle
	proxiedHandle *storage.ObjectHandle
	ctx           context.Context
}

// Add possible proxy option
type GCPBucketStorageProviderOptions struct {
	transportProxy *proxyTransport
}

func NewGCPBucketStorageProvider(ctx context.Context, bucketName string, proxyURL string) (*GCPBucketStorageProvider, error) {
	// Create the legacy GCP storage client with proper credentials
	client, err := storage.NewClient(ctx, option.WithEndpoint("private.googleapis.com"))
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	provider := &GCPBucketStorageProvider{
		bucket: client.Bucket(bucketName),
	}

	if proxyURL != "" {
		// We need to create a transport this way to set up the credentials.
		ht, err := htransport.NewTransport(ctx, newProxyTransport(proxyURL))
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy transport: %w", err)
		}

		httpClient := &http.Client{
			Transport: ht,
		}

		proxiedClient, err := storage.NewClient(ctx, option.WithHTTPClient(httpClient))
		if err != nil {
			return nil, fmt.Errorf("failed to create proxied GCS proxy client: %w", err)
		}

		provider.proxiedBucket = proxiedClient.Bucket(bucketName)
	}

	return provider, nil
}

func (g *GCPBucketStorageProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
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

func (g *GCPBucketStorageProvider) GetDetails() string {
	return fmt.Sprintf("[GCP Storage, bucket set to %s]", g.bucket.BucketName())
}

func (g *GCPBucketStorageProvider) OpenObject(ctx context.Context, path string) (StorageObjectProvider, error) {
	handle := g.bucket.Object(path).Retryer(
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

	// only one attempt for the proxied handle
	proxiedHandle := g.proxiedBucket.Object(path).Retryer(storage.WithPolicy(storage.RetryNever))

	return &GCPBucketStorageObjectProvider{
		storage:       g,
		path:          path,
		handle:        handle,
		proxiedHandle: proxiedHandle,
		ctx:           ctx,
	}, nil
}

func (g *GCPBucketStorageObjectProvider) Delete() error {
	ctx, cancel := context.WithTimeout(g.ctx, googleOperationTimeout)
	defer cancel()

	return g.handle.Delete(ctx)
}

func (g *GCPBucketStorageObjectProvider) Size() (int64, error) {
	ctx, cancel := context.WithTimeout(g.ctx, googleOperationTimeout)
	defer cancel()

	attrs, err := g.handle.Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get GCS object (%s) attributes: %w", g.path, err)
	}

	return attrs.Size, nil
}
func (g *GCPBucketStorageObjectProvider) ReadAt(buff []byte, off int64) (n int, err error) {
	if g.proxiedHandle == nil || true {
		return g.readAt(buff, off, false)
	}

	n, err = g.readAt(buff, off, true)
	if err != nil {
		zap.L().Warn("ReadAt: proxied handle failed, falling back to non-proxied handle", zap.String("path", g.path), zap.Bool("proxied", false), zap.Int64("off", off), zap.Int("len(buff)", len(buff)), zap.Error(err))
		n, err = g.readAt(buff, off, false)
		if err != nil {
			zap.L().Warn("ReadAt: non-proxied handle failed", zap.String("path", g.path), zap.Bool("proxied", false), zap.Int64("off", off), zap.Int("len(buff)", len(buff)), zap.Error(err))
		}
		return n, err
	}

	return n, nil
}

func (g *GCPBucketStorageObjectProvider) readAt(buff []byte, off int64, proxied bool) (n int, err error) {
	ctx, cancel := context.WithTimeout(g.ctx, googleReadTimeout)
	defer cancel()

	var reader *storage.Reader
	if proxied {
		reader, err = g.proxiedHandle.NewRangeReader(ctx, off, int64(len(buff)))
	} else {
		reader, err = g.handle.NewRangeReader(ctx, off, int64(len(buff)))
	}

	if err != nil {
		zap.L().Error("ReadAt: failed to create GCS reader", zap.String("path", g.path), zap.Bool("proxied", proxied), zap.Int64("off", off), zap.Int("len(buff)", len(buff)), zap.Error(err))
		return 0, fmt.Errorf("failed to create GCS reader: %w", err)
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

		return n, fmt.Errorf("failed to read from GCS object: %w", readErr)
	}

	return n, nil
}

func (g *GCPBucketStorageObjectProvider) ReadFrom(src io.Reader) (int64, error) {
	if g.proxiedHandle == nil || true {
		return g.readFrom(src, false)
	}

	n, err := g.readFrom(src, true)
	if err != nil {
		zap.L().Error("ReadFrom: proxied handle failed, falling back to non-proxied handle", zap.String("path", g.path), zap.Error(err))
		return g.readFrom(src, false)
	}
	return n, nil
}

func (g *GCPBucketStorageObjectProvider) readFrom(src io.Reader, proxied bool) (int64, error) {
	var w *storage.Writer
	if proxied {
		w = g.proxiedHandle.NewWriter(g.ctx)
	} else {
		w = g.handle.NewWriter(g.ctx)
	}

	n, err := io.Copy(w, src)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("failed to copy buffer to persistence: %w", err)
	}

	err = w.Close()
	if err != nil {
		return n, fmt.Errorf("failed to close GCS writer: %w", err)
	}

	return n, nil
}

func (g *GCPBucketStorageObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(g.ctx, googleReadTimeout)
	defer cancel()

	reader, err := g.handle.NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return 0, ErrorObjectNotExist
		}

		return 0, err
	}

	defer reader.Close()

	buff := make([]byte, googleBufferSize)
	n, err := io.CopyBuffer(dst, reader, buff)
	if err != nil {
		return n, fmt.Errorf("failed to copy GCS object to writer: %w", err)
	}

	return n, nil
}

func (g *GCPBucketStorageObjectProvider) WriteFromFileSystem(path string) error {
	cmd := exec.CommandContext(
		g.ctx,
		"gcloud",
		"storage",
		"cp",
		"--verbosity",
		"error",
		path,
		fmt.Sprintf("gs://%s/%s", g.storage.bucket.BucketName(), g.path),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to upload file to GCS: %w\n%s", err, string(output))
	}

	return nil
}
