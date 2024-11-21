package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

// How long to keep the template in the cache since the last access.
// Should be longer than the maximum possible sandbox lifetime.
const templateExpiration = time.Hour * 48

type TemplateCache struct {
	cache  *ttlcache.Cache[string, *Template]
	bucket *gcs.BucketHandle
	ctx    context.Context
}

func NewTemplateCache(ctx context.Context) *TemplateCache {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, *Template](templateExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Template]) {
		template := item.Value()

		err := template.Close()
		if err != nil {
			fmt.Printf("[template data cache]: failed to cleanup template data for item %s: %v\n", item.Key(), err)
		}
	})

	go cache.Start()

	return &TemplateCache{
		bucket: gcs.TemplateBucket,
		cache:  cache,
		ctx:    ctx,
	}
}

func (c *TemplateCache) GetTemplate(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) (*Template, error) {
	key := fmt.Sprintf("%s-%s", templateId, buildId)

	identifier, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate identifier: %w", err)
	}

	item, found := c.cache.GetOrSet(
		key,
		c.newTemplate(
			identifier.String(),
			templateId,
			buildId,
			kernelVersion,
			firecrackerVersion,
			hugePages,
		),
		ttlcache.WithTTL[string, *Template](templateExpiration),
	)

	template := item.Value()

	if !found {
		go template.Fetch(c.ctx, c.bucket)
	}

	return template, nil
}
