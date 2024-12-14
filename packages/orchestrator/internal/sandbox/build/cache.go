package build

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

const buildExpiration = time.Hour * 24

const cachePath = "/orchestrator/build"

type Store struct {
	bucket *gcs.BucketHandle
	cache  *ttlcache.Cache[string, *gcs.Object]
	ctx    context.Context
}

func NewStore(bucket *gcs.BucketHandle, ctx context.Context) *Store {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, *gcs.Object](buildExpiration),
	)

	go cache.Start()

	return &Store{
		bucket: bucket,
		cache:  cache,
		ctx:    ctx,
	}
}

func (s *Store) Get(id string) (*gcs.Object, error) {
	source, _ := s.cache.GetOrSet(
		id,
		gcs.NewObject(s.ctx, s.bucket, id),
		ttlcache.WithTTL[string, *gcs.Object](time.Hour*48),
	)

	value := source.Value()
	if value == nil {
		return nil, fmt.Errorf("failed to get source from cache: %s", id)
	}

	return value, nil
}
