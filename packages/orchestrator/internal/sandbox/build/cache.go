package build

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

const buildExpiration = time.Hour * 25

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
			zap.L().Warn("failed to cleanup build data for item", zap.String("item_key", item.Key()), zap.Error(err))
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

func (s *DiffStore) Get(buildId string, diffType DiffType, blockSize int64) (Diff, error) {
	diff := newStorageDiff(buildId, diffType, blockSize)

	source, found := s.cache.GetOrSet(
		diff.CacheKey(),
		diff,
		ttlcache.WithTTL[string, Diff](buildExpiration),
	)

	value := source.Value()
	if value == nil {
		return nil, fmt.Errorf("failed to get source from cache: %s", diff.CacheKey())
	}

	if !found {
		err := diff.Init(s.ctx, s.bucket)
		if err != nil {
			return nil, fmt.Errorf("failed to init source: %w", err)
		}
	}

	return value, nil
}

func (s *DiffStore) Add(buildId string, t DiffType, d Diff) {
	storagePath := storagePath(buildId, t)

	s.cache.Set(storagePath, d, buildExpiration)
}
