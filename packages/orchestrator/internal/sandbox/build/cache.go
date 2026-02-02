package build

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	ToMBShift        = 20
	fallbackDiffSize = 100 << ToMBShift
)

type deleteDiff struct {
	size      int64
	cancel    chan struct{}
	closeOnce sync.Once
}

type DiffStore struct {
	cachePath string
	cache     *ttlcache.Cache[DiffStoreKey, Diff]
	cancel    func()
	config    cfg.Config
	flags     *featureflags.Client

	// pdSizes is used to keep track of the diff sizes
	// that are scheduled for deletion, as this won't show up in the disk usage.
	pdSizes map[DiffStoreKey]*deleteDiff
	pdMu    sync.RWMutex
	pdDelay time.Duration
}

func NewDiffStore(
	config cfg.Config,
	flags *featureflags.Client,
	cachePath string,
	ttl, delay time.Duration,
) (*DiffStore, error) {
	err := os.MkdirAll(cachePath, 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	cache := ttlcache.New(
		ttlcache.WithTTL[DiffStoreKey, Diff](ttl),
	)

	ds := &DiffStore{
		cachePath: cachePath,
		cache:     cache,
		cancel:    func() {},
		config:    config,
		flags:     flags,
		pdSizes:   make(map[DiffStoreKey]*deleteDiff),
		pdDelay:   delay,
	}

	cache.OnEviction(func(ctx context.Context, _ ttlcache.EvictionReason, item *ttlcache.Item[DiffStoreKey, Diff]) {
		buildData := item.Value()
		// buildData will be deleted by calling buildData.Close()
		defer ds.resetDelete(item.Key())

		if closeErr := buildData.Close(); closeErr != nil {
			logger.L().Warn(ctx, "failed to cleanup build data cache for item", zap.Any("item_key", item.Key()), zap.Error(closeErr))
		}
	})

	return ds, nil
}

type DiffStoreKey string

func GetDiffStoreKey(buildID string, diffType DiffType) DiffStoreKey {
	return DiffStoreKey(fmt.Sprintf("%s/%s", buildID, diffType))
}

func (s *DiffStore) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	go s.cache.Start()
	go s.startDiskSpaceEviction(ctx, s.config, s.flags)
}

func (s *DiffStore) Close() {
	s.cancel()
	s.cache.Stop()
}

func (s *DiffStore) Get(_ context.Context, diff Diff) (Diff, error) {
	s.resetDelete(diff.CacheKey())
	source, _ := s.cache.GetOrSet(
		diff.CacheKey(),
		diff,
		ttlcache.WithTTL[DiffStoreKey, Diff](ttlcache.DefaultTTL),
	)

	value := source.Value()
	if value == nil {
		return nil, fmt.Errorf("failed to get source from cache: %s", diff.CacheKey())
	}

	// Chunker is lazily initialized on first ReadAt/Slice call using the frame table
	// from the mapping. No explicit Init() needed.
	return value, nil
}

func (s *DiffStore) Add(d Diff) {
	s.resetDelete(d.CacheKey())
	s.cache.Set(d.CacheKey(), d, ttlcache.DefaultTTL)
}

func (s *DiffStore) Has(d Diff) bool {
	return s.cache.Has(d.CacheKey())
}

func (s *DiffStore) startDiskSpaceEviction(
	ctx context.Context,
	config cfg.Config,
	flags *featureflags.Client,
) {
	services := cfg.GetServices(config)

	getDelay := func(fast bool) time.Duration {
		if fast {
			return time.Microsecond
		}

		return time.Second
	}

	timer := time.NewTimer(getDelay(false))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			dUsed, dTotal, err := diskUsage(s.cachePath)
			if err != nil {
				logger.L().Error(ctx, "failed to get disk usage", zap.Error(err))
				timer.Reset(getDelay(false))

				continue
			}

			pUsed := s.getPendingDeletesSize()
			used := int64(dUsed) - pUsed
			percentage := float64(used) / float64(dTotal) * 100

			threshold := featureflags.BuildCacheMaxUsagePercentage.Fallback()
			// When multiple services (template manager, orchestrator) are defined, take the lowest threshold
			// to ensure we don't exceed any of the set limits
			for _, s := range services {
				st := flags.IntFlag(ctx, featureflags.BuildCacheMaxUsagePercentage, featureflags.ServiceContext(string(s)))
				if st < threshold {
					threshold = st
				}
			}

			if percentage <= float64(threshold) {
				timer.Reset(getDelay(false))

				continue
			}

			succ, err := s.deleteOldestFromCache(ctx)
			if err != nil {
				logger.L().Error(ctx, "failed to delete oldest item from cache", zap.Error(err))
				timer.Reset(getDelay(false))

				continue
			}

			// Item evicted, reset timer to fast check
			timer.Reset(getDelay(succ))
		}
	}
}

func (s *DiffStore) getPendingDeletesSize() int64 {
	s.pdMu.RLock()
	defer s.pdMu.RUnlock()

	var pendingSize int64
	for _, value := range s.pdSizes {
		pendingSize += value.size
	}

	return pendingSize
}

// deleteOldestFromCache deletes the oldest item (smallest TTL) from the cache.
// ttlcache has items in order by TTL
func (s *DiffStore) deleteOldestFromCache(ctx context.Context) (suc bool, e error) {
	defer func() {
		// Because of bug in ttlcache RangeBackwards method, we need to handle potential panic until it gets fixed
		if r := recover(); r != nil {
			e = fmt.Errorf("recovered from panic in deleteOldestFromCache: %v", r)
			suc = false

			logger.L().Error(ctx, "recovered from panic in deleteOldestFromCache", zap.Error(e))
		}
	}()

	success := false
	s.cache.RangeBackwards(func(item *ttlcache.Item[DiffStoreKey, Diff]) bool {
		isDeleted := s.isBeingDeleted(item.Key())
		if isDeleted {
			return true
		}

		sfSize, err := item.Value().FileSize()
		if err != nil {
			logger.L().Warn(ctx, "failed to get size of deleted item from cache", zap.Error(err))
			sfSize = fallbackDiffSize
		}

		s.scheduleDelete(ctx, item.Key(), sfSize)

		success = true

		return false
	})

	return success, e
}

func (s *DiffStore) resetDelete(key DiffStoreKey) {
	s.pdMu.Lock()
	defer s.pdMu.Unlock()

	dDiff, f := s.pdSizes[key]
	if !f {
		return
	}

	dDiff.closeOnce.Do(func() {
		close(dDiff.cancel)
	})
	delete(s.pdSizes, key)
}

func (s *DiffStore) isBeingDeleted(key DiffStoreKey) bool {
	s.pdMu.RLock()
	defer s.pdMu.RUnlock()

	_, f := s.pdSizes[key]

	return f
}

func (s *DiffStore) scheduleDelete(ctx context.Context, key DiffStoreKey, dSize int64) {
	s.pdMu.Lock()
	defer s.pdMu.Unlock()

	cancelCh := make(chan struct{})
	s.pdSizes[key] = &deleteDiff{
		size:   dSize,
		cancel: cancelCh,
	}

	// Delay cache (file close/removal) deletion,
	// this is to prevent race conditions with exposed slices,
	// pending data fetching, or data upload
	go (func() {
		select {
		case <-ctx.Done():
		case <-cancelCh:
		case <-time.After(s.pdDelay):
			s.cache.Delete(key)
		}
	})()
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

func (s *DiffStore) RemoveCache() {
	s.cache.DeleteAll()
}
