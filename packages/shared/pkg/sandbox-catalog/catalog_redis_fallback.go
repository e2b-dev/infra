package sandbox_catalog

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

type RedisFallbackSandboxCatalog struct {
	sandboxCatalog             SandboxesCatalog
	redisFallbackCatalogClient *RedisSandboxCatalog
	featureFlags               *featureflags.Client
}

var _ SandboxesCatalog = (*RedisFallbackSandboxCatalog)(nil)

func (r *RedisFallbackSandboxCatalog) GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	redisSecurePrimary, err := r.featureFlags.BoolFlag(ctx, featureflags.ClientProxyRedisSecurePrimary)
	if err != nil {
		zap.L().Warn("failed to get feature flag", zap.Error(err))
	}

	if redisSecurePrimary {
		data, err := r.redisFallbackCatalogClient.GetSandbox(ctx, sandboxID)
		if err != nil {
			if errors.Is(err, ErrSandboxNotFound) {
				return r.sandboxCatalog.GetSandbox(ctx, sandboxID)
			}
		}

		return data, err
	}

	data, err := r.sandboxCatalog.GetSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			return r.redisFallbackCatalogClient.GetSandbox(ctx, sandboxID)
		}
	}

	return data, err
}

func (r *RedisFallbackSandboxCatalog) StoreSandbox(ctx context.Context, sandboxID string, sandboxInfo *SandboxInfo, expiration time.Duration) error {
	return r.sandboxCatalog.StoreSandbox(ctx, sandboxID, sandboxInfo, expiration)
}

func (r *RedisFallbackSandboxCatalog) DeleteSandbox(ctx context.Context, sandboxID string, executionID string) error {
	return r.sandboxCatalog.DeleteSandbox(ctx, sandboxID, executionID)
}

var _ SandboxesCatalog = (*RedisFallbackSandboxCatalog)(nil)

func NewRedisFallbackSandboxesCatalog(sandboxCatalog SandboxesCatalog, redisFallbackSandboxCatalog *RedisSandboxCatalog, featureFlagsClient *featureflags.Client) *RedisFallbackSandboxCatalog {
	return &RedisFallbackSandboxCatalog{
		sandboxCatalog:             sandboxCatalog,
		redisFallbackCatalogClient: redisFallbackSandboxCatalog,
		featureFlags:               featureFlagsClient,
	}
}

func (r *RedisFallbackSandboxCatalog) Close(ctx context.Context) error {
	var errs []error
	err := r.sandboxCatalog.Close(ctx)
	if err != nil {
		errs = append(errs, err)
	}

	err = r.redisFallbackCatalogClient.Close(ctx)
	if err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
