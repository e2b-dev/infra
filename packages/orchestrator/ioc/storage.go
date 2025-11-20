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

func newLimiter(lc fx.Lifecycle, featureFlags *featureflags.Client) (*limit.Limiter, error) {
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

func newTemplateCache(
	config cfg.Config,
	lc fx.Lifecycle,
	featureFlags *featureflags.Client,
	persistence storage.StorageProvider,
	blockMetrics blockmetrics.Metrics,
) (*template.Cache, error) {
	cache, err := template.NewCache(config, featureFlags, persistence, blockMetrics)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// the context passed in gets cancelled as soon as the
			// startup phase is done. we don't want that to propagate to
			// the background processes.
			ctx = context.WithoutCancel(ctx)
			cache.Start(ctx)

			return nil
		},
		OnStop: func(context.Context) error {
			cache.Shutdown()

			return nil
		},
	})

	return cache, nil
}

func newBlockMetrics(tel *telemetry.Client) (blockmetrics.Metrics, error) {
	return blockmetrics.NewMetrics(tel.MeterProvider)
}

func newPersistence(limiter *limit.Limiter) (storage.StorageProvider, error) {
	return storage.GetTemplateStorageProvider(context.Background(), limiter)
}
