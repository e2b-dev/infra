package build

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sys/unix"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

const buildExpiration = time.Hour * 25
const buildSpacePercentageExpiration = 90.0

const fileDeletionDelay = 60 * time.Second

const cachePath = "/orchestrator/build"

type DiffStore struct {
	bucket *gcs.BucketHandle
	cache  *ttlcache.Cache[string, Diff]
	ctx    context.Context

	// dCache is used to keep track of the diff sizes
	// that are scheduled for deletion, as this won't show up in the disk usage.
	dCache *ttlcache.Cache[string, int64]
}

func NewDiffStore(bucket *gcs.BucketHandle, ctx context.Context) (*DiffStore, error) {
	err := os.MkdirAll(cachePath, 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	cache := ttlcache.New(
		ttlcache.WithTTL[string, Diff](buildExpiration),
	)

	dCache := ttlcache.New(
		ttlcache.WithTTL[string, int64](fileDeletionDelay),
	)

	// Delay cache (file close/removal) deletion,
	// this is to prevent race conditions with exposed slices,
	// pending data fetching, or data upload
	dCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, int64]) {
		if reason != ttlcache.EvictionReasonExpired {
			return
		}

		cache.Delete(item.Key())
	})

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, Diff]) {
		buildData := item.Value()

		err = buildData.Close()
		if err != nil {
			zap.L().Warn("failed to cleanup build data cache for item", zap.String("item_key", item.Key()), zap.Error(err))
		}
	})

	ds := &DiffStore{
		bucket: bucket,
		cache:  cache,
		ctx:    ctx,
		dCache: dCache,
	}

	go cache.Start()
	go dCache.Start()
	go ds.startDiskSpaceEviction()

	return ds, nil
}

func (s *DiffStore) Get(buildId string, diffType DiffType, blockSize int64) (Diff, error) {
	diff := newStorageDiff(buildId, diffType, blockSize)

	s.resetDelete(diff.CacheKey())
	source, found := s.cache.GetOrSet(
		diff.CacheKey(),
		diff,
		ttlcache.WithTTL[string, Diff](ttlcache.DefaultTTL),
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

	s.resetDelete(storagePath)
	s.cache.Set(storagePath, d, ttlcache.DefaultTTL)
}

func (s *DiffStore) startDiskSpaceEviction() {
	getDelay := func(fast bool) time.Duration {
		if fast {
			return time.Microsecond
		} else {
			return time.Second
		}
	}

	timer := time.NewTimer(getDelay(false))
	defer timer.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
			dUsed, dTotal, err := diskUsage(cachePath)
			if err != nil {
				fmt.Printf("[build data cache]: failed to get disk usage: %v\n", err)
				timer.Reset(getDelay(false))
				continue
			}

			pUsed := s.getPendingDeletesSize()
			used := int64(dUsed) - pUsed
			percentage := float64(used) / float64(dTotal) * 100

			if percentage <= buildSpacePercentageExpiration {
				timer.Reset(getDelay(false))
				continue
			}

			succ, err := s.deleteOldestFromCache()
			if err != nil {
				fmt.Printf("[build data cache]: failed to delete oldest item from cache: %v\n", err)
				timer.Reset(getDelay(false))
				continue
			}

			// Item evicted, reset timer to fast check
			timer.Reset(getDelay(succ))
		}
	}
}

func (s *DiffStore) getPendingDeletesSize() int64 {
	var pendingSize int64
	s.dCache.Range(func(item *ttlcache.Item[string, int64]) bool {
		pendingSize += item.Value()
		return true
	})
	return pendingSize
}

// deleteOldestFromCache deletes the oldest item (smallest TTL) from the cache
// ttlcache has items in order by TTL
func (s *DiffStore) deleteOldestFromCache() (bool, error) {
	success := false
	var e error
	s.cache.RangeBackwards(func(item *ttlcache.Item[string, Diff]) bool {
		isDeleted := s.isBeingDeleted(item.Key())
		if isDeleted {
			return true
		}

		sfSize, err := item.Value().FileSize()
		if err != nil {
			e = fmt.Errorf("failed to get file size: %w", err)
			return false
		}

		s.scheduleDelete(item.Key(), sfSize)

		success = true
		return false
	})

	return success, e
}

func (s *DiffStore) resetDelete(key string) {
	s.dCache.Delete(key)
}

func (s *DiffStore) isBeingDeleted(key string) bool {
	return s.dCache.Has(key)
}

func (s *DiffStore) scheduleDelete(key string, dSize int64) {
	s.dCache.Set(key, dSize, ttlcache.DefaultTTL)
}

func diskUsage(path string) (uint64, uint64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get disk stats for path %s: %w", path, err)
	}

	// Available blocks * size per block = available space in bytes
	free := stat.Bavail * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)
	used := total - free

	return used, total, nil
}
