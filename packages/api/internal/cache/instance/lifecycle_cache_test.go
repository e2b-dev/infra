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
	expired *atomic.Bool
}

func (t *testLifecycleCacheItem) IsExpired() bool {
	return t.expired.Load()
}

func (t *testLifecycleCacheItem) SetExpired() {
	t.expired.Store(true)
}

func newCache(t *testing.T) (*lifecycleCache[*testLifecycleCacheItem], context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

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

func makeAtomicBool(value bool) *atomic.Bool {
	var result atomic.Bool
	result.Store(value)
	return &result
}

func TestLifecycleCacheSetIfAbsent(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, uint64(0), cache.Metrics().Evictions)
}

func TestLifecycleCacheGet(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	item, ok := cache.Get("test")
	assert.True(t, ok)
	assert.False(t, item.IsExpired())
	assert.Equal(t, 1, cache.Len())
}

func TestLifecycleCacheGetAndRemove(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	item, ok := cache.GetAndRemove("test")
	assert.True(t, ok)
	assert.True(t, item.IsExpired())
	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheRemove(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	ok := cache.Remove("test")
	assert.True(t, ok)
	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheItems(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	items := cache.Items()
	assert.Len(t, items, 1)
	for _, item := range items {
		assert.False(t, item.IsExpired())
	}
}

func TestLifecycleCacheLen(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	assert.Equal(t, 1, cache.Len())
}

func TestLifecycleCacheHasNonExpired(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	assert.True(t, cache.Has("test", false))
	assert.True(t, cache.Has("test", true))

	// Check for a non-existent key
	assert.False(t, cache.Has("non-existent", false))
	assert.False(t, cache.Has("non-existent", true))
}

func TestLifecycleCacheHasExpired(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	// Set the item as expired
	expired.Store(true)

	assert.False(t, cache.Has("test", false))
	assert.True(t, cache.Has("test", true))
}

func TestLifecycleCacheHasEvicting(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	evictCalled := make(chan struct{})
	cache.OnEviction(func(ctx context.Context, item *testLifecycleCacheItem) {
		// Simulate a slow eviction process
		time.Sleep(500 * time.Millisecond)
		close(evictCalled)
	})

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	// Set the item as expired
	expired.Store(true)

	// Wait for the eviction process to start but not complete
	time.Sleep(200 * time.Millisecond)

	assert.True(t, cache.Has("test", true))
	assert.False(t, cache.Has("test", false))

	// Wait for eviction to complete
	<-evictCalled
	// Wait for the eviction process (remove from an evicting map) to complete,
	// this delay is waiting just for code runtime.
	// Not ideal, but should be enough for most of the time
	time.Sleep(50 * time.Millisecond)

	assert.False(t, cache.Has("test", true))
	assert.False(t, cache.Has("test", false))
}

func TestLifecycleCacheOnEvictionCalled(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	evictCalled := false

	cache.OnEviction(func(ctx context.Context, item *testLifecycleCacheItem) {
		evictCalled = true
	})

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	expired.Store(true)

	time.Sleep(1 * time.Second)

	assert.True(t, evictCalled)
	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheEvictionNotCalledWhenItemIsNotExpired(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	time.Sleep(1 * time.Second)

	assert.Equal(t, 1, cache.Len())
}

func TestLifecycleCacheEvictionCalledWhenItemIsRemoved(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	cache.SetIfAbsent("test", &testLifecycleCacheItem{
		expired: expired,
	})

	cache.Remove("test")

	time.Sleep(1 * time.Second)

	assert.Equal(t, 0, cache.Len())
}

func TestLifecycleCacheManyItems(t *testing.T) {
	cache, cancel := newCache(t)
	defer cancel()

	expired := makeAtomicBool(false)
	for i := 0; i < 100; i++ {
		cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
			expired: expired,
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
			expired := makeAtomicBool(false)
			cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
				expired: expired,
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

	expired := makeAtomicBool(false)

	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
				expired: expired,
			})
		}(i)
	}
	wg.Wait()

	expired.Store(true)

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
			expired := makeAtomicBool(true)
			cache.SetIfAbsent(fmt.Sprintf("test-%d", i), &testLifecycleCacheItem{
				expired: expired,
			})
		}(i)
	}
	wg.Wait()

	time.Sleep(1 * time.Second)

	assert.Equal(t, int32(100), calledCount.Load())
}
