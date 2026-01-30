package template

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// How long to keep the template in the cache since the last access.
// Should be longer than the maximum possible sandbox lifetime.
const (
	templateExpiration = time.Hour * 25

	buildCacheTTL           = time.Hour * 25
	buildCacheDelayEviction = time.Second * 60
)

var (
	tracer     = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template")
	meter      = otel.GetMeterProvider().Meter("orchestrator.internal.sandbox.template")
	hitsMetric = utils.Must(meter.Int64Counter("orchestrator.templates.cache.hits",
		metric.WithDescription("Requests for templates that were already cached")))
	missesMetric = utils.Must(meter.Int64Counter("orchestrator.templates.cache.misses",
		metric.WithDescription("Requests for templates that were not cached")))
)

type Cache struct {
	config        cfg.BuilderConfig
	flags         *featureflags.Client
	cache         *ttlcache.Cache[string, Template]
	persistence   storage.StorageProvider
	buildStore    *build.DiffStore
	blockMetrics  blockmetrics.Metrics
	rootCachePath string
}

// NewCache initializes a template new cache.
// It also deletes the old build cache directory content
// as it may contain stale data that are not managed by anyone.
func NewCache(
	config cfg.Config,
	flags *featureflags.Client,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
) (*Cache, error) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	cache.OnEviction(func(ctx context.Context, _ ttlcache.EvictionReason, item *ttlcache.Item[string, Template]) {
		template := item.Value()

		err := template.Close(ctx)
		if err != nil {
			logger.L().Warn(ctx, "failed to cleanup template data", zap.String("item_key", item.Key()), zap.Error(err))
		}
	})

	// Delete the old build cache directory content.
	err := cleanDir(config.DefaultCacheDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove old build cache directory: %w", err)
	}

	buildStore, err := build.NewDiffStore(
		config,
		flags,
		config.DefaultCacheDir,
		buildCacheTTL,
		buildCacheDelayEviction,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create build store: %w", err)
	}

	return &Cache{
		blockMetrics:  metrics,
		config:        config.BuilderConfig,
		persistence:   persistence,
		buildStore:    buildStore,
		cache:         cache,
		flags:         flags,
		rootCachePath: config.BuilderConfig.SharedChunkCacheDir,
	}, nil
}

func (c *Cache) Start(ctx context.Context) {
	c.buildStore.Start(ctx)

	go c.cache.Start()
}

func (c *Cache) Stop() {
	c.buildStore.Close()
	c.cache.Stop()
}

func (c *Cache) Items() map[string]*ttlcache.Item[string, Template] {
	return c.cache.Items()
}

// Invalidate removes a template from the cache, forcing a refetch on next access.
func (c *Cache) Invalidate(buildID string) {
	c.cache.Delete(buildID)
}

// InvalidateAll clears all cached templates and build diffs.
// Used for cold start benchmarks to ensure no cached data is reused.
func (c *Cache) InvalidateAll() {
	c.cache.DeleteAll()
	c.buildStore.RemoveCache()
}

func (c *Cache) GetTemplate(
	ctx context.Context,
	buildID string,
	isSnapshot bool,
	isBuilding bool,
) (Template, error) {
	ctx, span := tracer.Start(ctx, "get template", trace.WithAttributes(
		attribute.Bool("is_snapshot", isSnapshot),
		attribute.Bool("is_building", isBuilding),
	))
	defer span.End()

	persistence := c.persistence
	// Because of the template caching, if we enable the NFS cache feature flag,
	// it will start working only for new orchestrators or new builds.
	if path, enabled := c.useNFSCache(ctx, isBuilding, isSnapshot); enabled {
		logger.L().Info(ctx, "using local template cache", zap.String("path", c.rootCachePath))
		persistence = storage.WrapInNFSCache(ctx, path, persistence, c.flags)
		span.SetAttributes(attribute.Bool("use_cache", true))
	} else {
		span.SetAttributes(attribute.Bool("use_cache", false))
	}

	storageTemplate, err := newTemplateFromStorage(
		c.config,
		buildID,
		nil,
		nil,
		persistence,
		c.blockMetrics,
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	return c.getTemplateWithFetch(ctx, storageTemplate), nil
}

func (c *Cache) AddSnapshot(
	ctx context.Context,
	buildId string,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	localSnapfile File,
	localMetafile File,
	memfileDiff build.Diff,
	rootfsDiff build.Diff,
) error {
	switch memfileDiff.(type) {
	case *build.NoDiff:
	default:
		c.buildStore.Add(memfileDiff)
	}

	switch rootfsDiff.(type) {
	case *build.NoDiff:
	default:
		c.buildStore.Add(rootfsDiff)
	}

	storageTemplate, err := newTemplateFromStorage(
		c.config,
		buildId,
		memfileHeader,
		rootfsHeader,
		c.persistence,
		c.blockMetrics,
		localSnapfile,
		localMetafile,
	)
	if err != nil {
		return fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	c.getTemplateWithFetch(ctx, storageTemplate)

	return nil
}

func (c *Cache) useNFSCache(_ context.Context, _ bool, _ bool) (string, bool) {
	return "", false

	// if isBuilding {
	// 	// caching this layer doesn't speed up the next sandbox launch,
	// 	// as the previous template isn't used to load the one that's being built.
	// 	return "", false
	// }

	// var flagName featureflags.BoolFlag
	// if isSnapshot {
	// 	flagName = featureflags.SnapshotFeatureFlag
	// } else {
	// 	flagName = featureflags.TemplateFeatureFlag
	// }

	// useNFSCache := c.flags.BoolFlag(ctx, flagName)
	// if useNFSCache {
	// 	if c.rootCachePath == "" {
	// 		logger.L().Warn(ctx, "NFSCache feature flag is enabled but cache path is not set")

	// 		return "", false
	// 	}
	// }

	// return c.rootCachePath, useNFSCache
}

func cleanDir(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading directory contents: %w", err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("removing %q: %w", entryPath, err)
		}
	}

	return nil
}

func (c *Cache) getTemplateWithFetch(ctx context.Context, storageTemplate *storageTemplate) Template {
	t, found := c.cache.GetOrSet(
		storageTemplate.Files().CacheKey(),
		storageTemplate,
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	if !found {
		missesMetric.Add(ctx, 1)
		// We don't want to cancel the request if the request was canceled, because it can be used by other templates
		// It's a little bit problematic, because shutdown won't cancel the fetch
		go storageTemplate.Fetch(context.WithoutCancel(ctx), c.buildStore)
	} else {
		hitsMetric.Add(ctx, 1)
	}

	return t.Value()
}
