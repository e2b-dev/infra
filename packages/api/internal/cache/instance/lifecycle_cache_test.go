package instance

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testLifecycleCacheItem struct {
	expired *bool
}

func (t *testLifecycleCacheItem) IsExpired() bool {
	return *t.expired
}

func (t *testLifecycleCacheItem) SetExpired() {
	*t.expired = true
}

func newCache(t *testing.T) (*lifecycleCache[*testLifecycleCacheItem], context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	cache := newLifecycleCache[*testLifecycleCacheItem]()
	go cache.Start(ctx)

	return cache, cancel
}

func TestLifecycleCacheInit(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	assert.Equal(t, 0, cache.Len())
	assert.Equal(t, uint64(0), cache.Metrics().Evictions)
}

func TestLifecycleCacheSetIfAbsent(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, uint64(0), cache.Metrics().Evictions)
}

func TestLifecycleCacheGet(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	item, ok := cache.Get("test")
	assert.Equal(t, true, ok)
	assert.Equal(t, false, item.IsExpired())
	assert.Equal(t, 1, cache.Len())
}

func TestLifecycleCacheGetAndRemove(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	item, ok := cache.GetAndRemove("test")
	assert.Equal(t, true, ok)
	assert.Equal(t, true, item.IsExpired())
	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheRemove(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	ok := cache.Remove("test")
	assert.Equal(t, true, ok)
	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheItems(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	items := cache.Items()
	assert.Equal(t, 1, len(items))
	for _, item := range items {
		assert.Equal(t, false, item.IsExpired())
	}
}

func TestLifecycleCacheLen(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	assert.Equal(t, 1, cache.Len())
}

func TestLifecycleCacheOnEvictionCalled(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	evictCalled := false

	cache.OnEviction(func(ctx context.Context, item *testLifecycleCacheItem) {
		evictCalled = true
	})

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	expired = true

	time.Sleep(1 * time.Second)

	assert.True(t, evictCalled)
	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheEvictionNotCalledWhenItemIsNotExpired(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	time.Sleep(1 * time.Second)

	assert.Equal(t, 1, cache.Len())
}

func TestLifecycleCacheEvictionCalledWhenItemIsRemoved(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: &expired,
	})

	cache.Remove("test")

	time.Sleep(1 * time.Second)

	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheManyItems(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false
	for i := 0; i < 100; i++ {
		cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
			expired: &expired,
		})
	}

	assert.Equal(t, 100, cache.Len())

	for i := 0; i < 100; i++ {
		cache.Remove(fmt.Sprintf("test-%d", i))
	}

	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheConcurrentAccess(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			expired := false
			cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
				expired: &expired,
			})
		}(i)
	}
	wg.Wait()

	wg = sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.Get(fmt.Sprintf("test-%d", i))
		}(i)
	}
	wg.Wait()

	wg = sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.Remove(fmt.Sprintf("test-%d", i))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheConcurrentEviction(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := false

	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
				expired: &expired,
			})
		}(i)
	}
	wg.Wait()

	expired = true

	time.Sleep(1 * time.Second)

	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheConcurrentEvictionOnEviction(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	calledCount := atomic.Int32{}
	cache.OnEviction(func(ctx context.Context, item *testLifecycleCacheItem) {
		calledCount.Add(1)
	})

	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			expired := true
			cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
				expired: &expired,
			})
		}(i)
	}
	wg.Wait()

	time.Sleep(1 * time.Second)

	assert.Equal(t, int32(100), calledCount.Load())
}
