package template

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

// How long to keep the template in the cache since the last access.
// Should be longer than the maximum possible sandbox lifetime.
const templateExpiration = time.Hour * 48

type Cache struct {
	cache  *ttlcache.Cache[string, Template]
	bucket *gcs.BucketHandle
	ctx    context.Context
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

	return &Cache{
		bucket: gcs.TemplateBucket,
		cache:  cache,
		ctx:    ctx,
	}
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
		go storageTemplate.Fetch(c.ctx, c.bucket)
	}

	return t.Value(), nil
}
