package template

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// How long to keep the template in the cache since the last access.
// Should be longer than the maximum possible sandbox lifetime.
const (
	templateExpiration = time.Hour * 25

	buildCacheTTL           = time.Hour * 25
	buildCacheDelayEviction = time.Second * 60

	// buildCacheMaxUsedPercentage the maximum percentage of the cache disk storage
	// that can be used before the cache starts evicting items.
	buildCacheMaxUsedPercentage = 75.0
)

type Cache struct {
	cache        *ttlcache.Cache[string, Template]
	persistence  storage.StorageProvider
	ctx          context.Context
	buildStore   *build.DiffStore
	blockMetrics blockmetrics.Metrics
}

// NewCache initializes a template new cache.
// It also deletes the old build cache directory content
// as it may contain stale data that are not managed by anyone.
func NewCache(ctx context.Context, persistence storage.StorageProvider, metrics blockmetrics.Metrics) (*Cache, error) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, Template]) {
		template := item.Value()

		err := template.Close()
		if err != nil {
			zap.L().Warn("failed to cleanup template data", zap.String("item_key", item.Key()), zap.Error(err))
		}
	})

	// Delete the old build cache directory content.
	err := cleanDir(build.DefaultCachePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove old build cache directory: %w", err)
	}

	buildStore, err := build.NewDiffStore(
		ctx,
		build.DefaultCachePath,
		buildCacheTTL,
		buildCacheDelayEviction,
		buildCacheMaxUsedPercentage,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create build store: %w", err)
	}

	go cache.Start()

	return &Cache{
		blockMetrics: metrics,
		persistence:  persistence,
		buildStore:   buildStore,
		cache:        cache,
		ctx:          ctx,
	}, nil
}

func (c *Cache) Items() map[string]*ttlcache.Item[string, Template] {
	return c.cache.Items()
}

func (c *Cache) GetTemplate(
	buildID,
	kernelVersion,
	firecrackerVersion string,
) (Template, error) {
	storageTemplate, err := newTemplateFromStorage(
		buildID,
		kernelVersion,
		firecrackerVersion,
		nil,
		nil,
		c.persistence,
		c.blockMetrics,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	t, found := c.cache.GetOrSet(
		storageTemplate.Files().CacheKey(),
		storageTemplate,
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	if !found {
		go storageTemplate.Fetch(c.ctx, c.buildStore)
	}

	return t.Value(), nil
}

func (c *Cache) AddSnapshot(
	buildId,
	kernelVersion,
	firecrackerVersion string,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	localSnapfile Snapfile,
	memfileDiff build.Diff,
	rootfsDiff build.Diff,
) error {
	switch memfileDiff.(type) {
	case *build.NoDiff:
		break
	default:
		c.buildStore.Add(memfileDiff)
	}

	switch rootfsDiff.(type) {
	case *build.NoDiff:
		break
	default:
		c.buildStore.Add(rootfsDiff)
	}

	storageTemplate, err := newTemplateFromStorage(
		buildId,
		kernelVersion,
		firecrackerVersion,
		memfileHeader,
		rootfsHeader,
		c.persistence,
		c.blockMetrics,
		localSnapfile,
	)
	if err != nil {
		return fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	_, found := c.cache.GetOrSet(
		storageTemplate.Files().CacheKey(),
		storageTemplate,
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	if !found {
		go storageTemplate.Fetch(c.ctx, c.buildStore)
	}

	return nil
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
