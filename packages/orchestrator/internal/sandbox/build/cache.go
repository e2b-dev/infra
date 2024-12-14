package build

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

const buildExpiration = time.Hour * 24

const cachePath = "/orchestrator/build"

type DiffStore struct {
	bucket *gcs.BucketHandle
	cache  *ttlcache.Cache[string, Diff]
	ctx    context.Context
}

func NewDiffStore(bucket *gcs.BucketHandle, ctx context.Context) (*DiffStore, error) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, Diff](buildExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, Diff]) {
		buildData := item.Value()

		err := buildData.Close()
		if err != nil {
			fmt.Printf("[build data cache]: failed to cleanup build data for item %s: %v\n", item.Key(), err)
		}
	})

	err := os.MkdirAll(cachePath, 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	go cache.Start()

	return &DiffStore{
		bucket: bucket,
		cache:  cache,
		ctx:    ctx,
	}, nil
}

func (s *DiffStore) Get(id string, blockSize int64) (Diff, error) {
	source, found := s.cache.GetOrSet(
		id,
		newStorageDiff(s.ctx, s.bucket, id, blockSize),
		ttlcache.WithTTL[string, Diff](buildExpiration),
	)

	value := source.Value()
	if value == nil {
		return nil, fmt.Errorf("failed to get source from cache: %s", id)
	}

	if !found {
		err := value.Init()
		if err != nil {
			return nil, fmt.Errorf("failed to init source: %w", err)
		}
	}

	return value, nil
}
