package ioc

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func NewLimiter(lc fx.Lifecycle, featureFlags *featureflags.Client) (*limit.Limiter, error) {
	limiter, err := limit.New(context.Background(), featureFlags)
	if err != nil {
		return nil, fmt.Errorf("failed to create limiter: %w", err)
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return limiter.Close(ctx)
		},
	})

	return limiter, nil
}

func NewTemplateCache(
	config cfg.Config,
	featureFlags *featureflags.Client,
	persistence storage.StorageProvider,
	blockMetrics blockmetrics.Metrics,
) (*template.Cache, error) {
	return template.NewCache(context.Background(), config, featureFlags, persistence, blockMetrics)
}

func NewBlockMetrics(tel *telemetry.Client) (blockmetrics.Metrics, error) {
	return blockmetrics.NewMetrics(tel.MeterProvider)
}

func NewPersistence(limiter *limit.Limiter) (storage.StorageProvider, error) {
	return storage.GetTemplateStorageProvider(context.Background(), limiter)
}
