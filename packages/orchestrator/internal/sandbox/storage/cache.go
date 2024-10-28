package storage

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"github.com/jellydator/ttlcache/v3"
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

	item, _ := t.cache.GetOrSet(
		key,
		t.newTemplate(templateId, buildId, kernelVersion, firecrackerVersion, hugePages),
		ttlcache.WithTTL[string, *Template](templateDataExpiration),
	)

	template := item.Value()
	if template == nil {
		t.cache.Delete(key)

		return nil, fmt.Errorf("failed to create template data cache %s", key)
	}

	return template, nil
}
