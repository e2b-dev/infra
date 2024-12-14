package template

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

// How long to keep the template in the cache since the last access.
// Should be longer than the maximum possible sandbox lifetime.
const templateExpiration = time.Hour * 24

type Cache struct {
	cache      *ttlcache.Cache[string, Template]
	bucket     *gcs.BucketHandle
	ctx        context.Context
	buildStore *build.Store
}

func NewCache(ctx context.Context) *Cache {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, Template]) {
		template := item.Value()

		err := template.Close()
		if err != nil {
			fmt.Printf("[template data cache]: failed to cleanup template data for item %s: %v\n", item.Key(), err)
		}
	})

	go cache.Start()

	buildStore := build.NewStore(gcs.TemplateBucket, ctx)

	return &Cache{
		bucket:     gcs.TemplateBucket,
		buildStore: buildStore,
		cache:      cache,
		ctx:        ctx,
	}
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
	isSnapshot bool,
) (Template, error) {
	storageTemplate, err := newTemplateFromStorage(
		templateId,
		buildId,
		kernelVersion,
		firecrackerVersion,
		hugePages,
		isSnapshot,
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

func (c *Cache) RemoveTemplate(templateId string, buildId string) {
	c.cache.Delete(fmt.Sprintf("%s-%s", templateId, buildId))
}

func (c *Cache) AddTemplateWithPrefetch(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
	isSnapshot bool,
) (func(), error) {
	storageTemplate, err := newTemplateFromStorage(
		templateId,
		buildId,
		kernelVersion,
		firecrackerVersion,
		hugePages,
		isSnapshot,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache from storage: %w", err)
	}

	_, found := c.cache.GetOrSet(
		storageTemplate.Files().CacheKey(),
		storageTemplate,
		ttlcache.WithTTL[string, Template](templateExpiration),
	)

	if !found {
		return func() {
			storageTemplate.Fetch(c.ctx, c.buildStore)
		}, nil
	}

	return func() {}, nil
}
