package storage

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
)

// Option configures provider construction (see NewProvider).
type Option func(*providerOptions)

type providerOptions struct {
	limiter       *limit.Limiter
	uploadBaseURL string
	hmacKey       []byte
}

// WithLimiter sets the concurrent-upload limiter used by the cloud providers.
func WithLimiter(limiter *limit.Limiter) Option {
	return func(o *providerOptions) { o.limiter = limiter }
}

// WithLocalUpload configures the filesystem provider to sign upload URLs.
func WithLocalUpload(uploadBaseURL string, hmacKey []byte) Option {
	return func(o *providerOptions) {
		o.uploadBaseURL = uploadBaseURL
		o.hmacKey = hmacKey
	}
}

// NewProvider constructs the storage provider for a resolved destination.
func NewProvider(ctx context.Context, spec Spec, opts ...Option) (StorageProvider, error) {
	var o providerOptions
	for _, opt := range opts {
		opt(&o)
	}

	switch spec.Provider {
	case LocalStorageProvider:
		return newFileSystemStorage(spec.BasePath, o.uploadBaseURL, o.hmacKey), nil
	case AWSStorageProvider:
		return newAWSStorage(ctx, spec, o.limiter)
	case GCPStorageProvider:
		return NewGCP(ctx, spec.Bucket, o.limiter)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", spec.Provider)
}
