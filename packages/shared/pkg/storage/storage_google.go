package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
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

	gcsOperationAttr       = "operation"
	gcsOperationAttrReadAt = "ReadAt"
	gcsOperationAttrWrite  = "Write"
	gcsOperationAttrStore  = "Store"
	gcsOperationAttrUpload = "WriteFromFileSystemOneShot"
)

var googleWriteTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
	"orchestrator.storage.gcs.write",
	"Duration of GCS writes",
	"Total bytes written to GCS",
	"Total writes to GCS",
))

type GCP struct {
	client  *storage.Client
	bucket  *storage.BucketHandle
	limiter *limit.Limiter

	baseUploadURL string // for testing
}

func newGCPBackend(ctx context.Context, bucketName string, limiter *limit.Limiter) (*Backend, error) {
	client, err := storage.NewGRPCClient(ctx,
		option.WithGRPCConnectionPool(4),
		option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(32*megabyte)),
		option.WithGRPCDialOption(grpc.WithInitialWindowSize(4*megabyte)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP client: %w", err)
	}

	gcp := &GCP{
		client:  client,
		bucket:  client.Bucket(bucketName),
		limiter: limiter,

		baseUploadURL: fmt.Sprintf("https://%s.storage.googleapis.com", bucketName),
	}

	return &Backend{
		Basic:                    gcp,
		Manager:                  gcp,
		MultipartUploaderFactory: gcp,
		PublicUploader:           gcp,
		RangeGetter:              gcp,
	}, nil
}

func (g *GCP) String() string {
	return fmt.Sprintf("[GCP Storage, bucket set to %s]", g.bucket.BucketName())
}

func (g *GCP) DeleteWithPrefix(ctx context.Context, prefix string) error {
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

func (g *GCP) PublicUploadURL(_ context.Context, path string, ttl time.Duration) (string, error) {
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

func (g *GCP) handle(path string) *storage.ObjectHandle {
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

func (g *GCP) RawSize(ctx context.Context, path string) (n int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	h := g.handle(path)
	attrs, err := h.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", path, ErrObjectNotExist)
		}

		return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", path, err)
	}

	return attrs.Size, nil
}

func (g *GCP) Upload(ctx context.Context, path string, in io.Reader) (n int64, e error) {
	timer := googleWriteTimerFactory.Begin(
		attribute.String(gcsOperationAttr, gcsOperationAttrWrite))

	w := g.handle(path).NewWriter(ctx)
	defer func() {
		if err := w.Close(); err != nil {
			e = errors.Join(e, fmt.Errorf("failed to write to %q: %w", path, err))
		}
	}()

	c, err := io.Copy(w, in)
	if ignoreEOF(err) != nil {
		timer.Failure(ctx, c)

		return c, fmt.Errorf("failed to write to %q: %w", path, err)
	}

	timer.Success(ctx, c)

	return c, nil
}

type withCancelCloser struct {
	io.ReadCloser

	cancelFunc context.CancelFunc
}

func (c withCancelCloser) Close() error {
	if c.cancelFunc != nil {
		c.cancelFunc()
	}

	return c.ReadCloser.Close()
}

func (g *GCP) StartDownload(ctx context.Context, path string) (rc io.ReadCloser, err error) {
	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)

	rc, err = g.handle(path).NewReader(ctx)
	if err != nil {
		cancel()
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, fmt.Errorf("failed to create reader for %q: %w", path, ErrObjectNotExist)
		}

		return nil, fmt.Errorf("failed to create reader for %q: %w", path, err)
	}

	return withCancelCloser{ReadCloser: rc, cancelFunc: cancel}, nil
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

// RangeGet fetches bytes from storage at C (compressed) offset.
func (g *GCP) RangeGet(ctx context.Context, path string, offset int64, length int) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, googleReadTimeout)

	rc, err := g.handle(path).NewRangeReader(ctx, offset, int64(length))
	if err != nil {
		cancel()
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, fmt.Errorf("failed to create range reader for %q: %w", path, ErrObjectNotExist)
		}

		return nil, fmt.Errorf("failed to create range reader for %q: %w", path, err)
	}

	return withCancelCloser{ReadCloser: rc, cancelFunc: cancel}, nil
}

func (g *GCP) Size(ctx context.Context, path string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	h := g.handle(path)
	attrs, err := h.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", path, ErrObjectNotExist)
		}

		return 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", path, err)
	}

	// Check for uncompressed size in metadata (set during compressed upload).
	if attrs.Metadata != nil {
		if uncompressedStr, ok := attrs.Metadata[MetadataKeyUncompressedSize]; ok {
			var uncompressedSize int64
			if _, err := fmt.Sscanf(uncompressedStr, "%d", &uncompressedSize); err == nil {
				return uncompressedSize, nil
			}
		}
	}

	return attrs.Size, nil
}

// Sizes returns both virtual (U) and raw (C) sizes for an object.
func (g *GCP) Sizes(ctx context.Context, path string) (virtSize, rawSize int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, googleOperationTimeout)
	defer cancel()

	h := g.handle(path)
	attrs, err := h.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return 0, 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", path, ErrObjectNotExist)
		}

		return 0, 0, fmt.Errorf("failed to get GCS object (%q) attributes: %w", path, err)
	}

	rawSize = attrs.Size

	// Check for uncompressed size in metadata (set during compressed upload).
	if attrs.Metadata != nil {
		if uncompressedStr, ok := attrs.Metadata[MetadataKeyUncompressedSize]; ok {
			if _, err := fmt.Sscanf(uncompressedStr, "%d", &virtSize); err == nil {
				return virtSize, rawSize, nil
			}
		}
	}

	return rawSize, rawSize, nil
}
