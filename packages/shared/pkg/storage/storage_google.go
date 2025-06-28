package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/iterator"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
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
	client *storage.Client
	bucket *storage.BucketHandle
}

type GCPBucketStorageObjectProvider struct {
	storage *GCPBucketStorageProvider
	path    string
	handle  *storage.ObjectHandle
	ctx     context.Context
}

func NewGCPBucketStorageProvider(ctx context.Context, bucketName string) (*GCPBucketStorageProvider, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &GCPBucketStorageProvider{
		client: client,
		bucket: client.Bucket(bucketName),
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

func (g *GCPBucketStorageProvider) UploadSignedURL(_ context.Context, path string, ttl time.Duration) (string, error) {
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

type gCPServiceToken struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

func parseServiceAccountBase64(serviceAccount string) (*gCPServiceToken, error) {
	decoded, err := base64.StdEncoding.DecodeString(serviceAccount)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	var sa gCPServiceToken
	if err := json.Unmarshal(decoded, &sa); err != nil {
		return nil, fmt.Errorf("failed to parse service account JSON: %w", err)
	}

	return &sa, nil
}
