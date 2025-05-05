package template

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"

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
	cache       *ttlcache.Cache[string, Template]
	persistence storage.StorageProvider
	ctx         context.Context
	buildStore  *build.DiffStore
}

func NewCache(ctx context.Context) (*Cache, error) {
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

	go cache.Start()

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

	persistence, err := storage.GetTemplateStorageProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get storage provider: %w", err)
	}

	return &Cache{
		persistence: persistence,
		buildStore:  buildStore,
		cache:       cache,
		ctx:         ctx,
	}, nil
}

func (c *Cache) Items() map[string]*ttlcache.Item[string, Template] {
	return c.cache.Items()
}

func (c *Cache) GetTemplate(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) (Template, error) {
	storageTemplate, err := newTemplateFromStorage(
		templateId,
		buildId,
		kernelVersion,
		firecrackerVersion,
		hugePages,
		nil,
		nil,
		c.persistence,
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
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	localSnapfile *LocalFile,
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
		templateId,
		buildId,
		kernelVersion,
		firecrackerVersion,
		hugePages,
		memfileHeader,
		rootfsHeader,
		c.persistence,
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
