package storage

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"

	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
)

type TemplateCache struct {
	cache   *ttlcache.Cache[string, *Template]
	bucket  *storage.BucketHandle
	nbdPool *nbd.DevicePool
	ctx     context.Context
}

func NewTemplateCache(ctx context.Context, client *storage.Client, bucket string, nbdPool *nbd.DevicePool) *TemplateCache {
	b := client.Bucket(bucket)

	cache := ttlcache.New(
		ttlcache.WithTTL[string, *Template](templateDataExpiration),
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
		bucket:  b,
		cache:   cache,
		ctx:     ctx,
		nbdPool: nbdPool,
	}
}

func (t *TemplateCache) GetTemplate(
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

	item, found := t.cache.GetOrSet(
		key,
		t.newTemplate(
			identifier.String(),
			templateId,
			buildId,
			kernelVersion,
			firecrackerVersion,
			hugePages,
		),
		ttlcache.WithTTL[string, *Template](templateDataExpiration),
	)

	template := item.Value()
	if template == nil {
		t.cache.Delete(key)

		return nil, fmt.Errorf("failed to create template data cache %s", key)
	}

	if !found {
		go template.Fetch(t.ctx, t.bucket)
	}

	return template, nil
}
