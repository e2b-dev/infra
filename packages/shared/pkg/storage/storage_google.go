package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
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
	client         *storage.Client
	bucket         *storage.BucketHandle
	proxyTransport *proxyTransport
}

type GCPBucketStorageObjectProvider struct {
	storage *GCPBucketStorageProvider
	path    string
	handle  *storage.ObjectHandle
	ctx     context.Context
}

// Add possible proxy option
type GCPBucketStorageProviderOptions struct {
	transportProxy *proxyTransport
}

type GCPBucketStorageProviderOption func(*GCPBucketStorageProviderOptions)

func WithProxy(proxyURL string) GCPBucketStorageProviderOption {
	return func(o *GCPBucketStorageProviderOptions) {
		o.transportProxy = newProxyTransport(proxyURL)
	}
}

func NewGCPBucketStorageProvider(ctx context.Context, bucketName string, opts ...GCPBucketStorageProviderOption) (*GCPBucketStorageProvider, error) {
	var providerOptions GCPBucketStorageProviderOptions

	for _, opt := range opts {
		opt(&providerOptions)
	}

	var clientOptions []option.ClientOption

	if providerOptions.transportProxy != nil {
		// We need to create a transport this way to set up the credentials.
		ht, err := htransport.NewTransport(ctx, providerOptions.transportProxy)
		if err != nil {
			return nil, fmt.Errorf("failed to create GCS client: %w", err)
		}

		httpClient := &http.Client{
			Transport: ht,
		}

		clientOptions = append(clientOptions, option.WithHTTPClient(httpClient))
	}

	// Create the storage client with proper credentials
	client, err := storage.NewClient(ctx, clientOptions...)

	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &GCPBucketStorageProvider{
		client:         client,
		bucket:         client.Bucket(bucketName),
		proxyTransport: providerOptions.transportProxy,
	}, nil
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
		storage.WithPolicy(storage.RetryAlways),
		storage.WithErrorFunc(func(err error) bool {
			if err == nil || errors.Is(err, storage.ErrObjectNotExist) {
				return storage.ShouldRetry(err)
			}

			fmt.Fprintf(os.Stderr, "Failed to do the request: %s\n", err)

			if g.proxyTransport != nil {
				g.proxyTransport.disableProxy()
			}

			return storage.ShouldRetry(err)
		}),
	)

	return &GCPBucketStorageObjectProvider{
		storage: g,
		path:    path,
		handle:  handle,
		ctx:     ctx,
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
	ctx, cancel := context.WithTimeout(g.ctx, googleReadTimeout)
	defer cancel()

	// The file should not be gzip compressed
	reader, err := g.handle.NewRangeReader(ctx, off, int64(len(buff)))
	if err != nil {
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
	w := g.handle.NewWriter(g.ctx)

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
