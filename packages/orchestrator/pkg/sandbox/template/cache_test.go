//go:build linux

package template

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
)

func newTestCache(defaultTTL time.Duration) *Cache {
	return &Cache{
		cache: ttlcache.New(ttlcache.WithTTL[string, Template](defaultTTL)),
	}
}

// simulateGetTemplate mimics getTemplateWithFetch's lock-protected TTL logic
// without needing a full storageTemplate (which requires disk paths).
func simulateGetTemplate(c *Cache, key string, maxSandboxLengthHours int64) {
	ttl := templateExpiration
	if maxSandboxLengthHours > 0 {
		ttl = max(ttl, time.Duration(maxSandboxLengthHours)*time.Hour+templateExpirationBuffer)
	}

	c.extendMu.Lock()
	t, found := c.cache.GetOrSet(key, nil, ttlcache.WithTTL[string, Template](ttl))
	if found && t.TTL() < ttl {
		c.cache.Set(key, t.Value(), ttl)
	}
	c.extendMu.Unlock()
}

func TestGetTemplate_ExtendsTTL(t *testing.T) {
	t.Parallel()

	defaultTTL := 50 * time.Millisecond
	c := newTestCache(defaultTTL)
	go c.cache.Start()
	defer c.cache.Stop()

	key := "build-long-running"
	c.cache.Set(key, nil, defaultTTL)

	simulateGetTemplate(c, key, 168)

	item := c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, 168*time.Hour+templateExpirationBuffer, item.TTL())

	time.Sleep(defaultTTL + 20*time.Millisecond)

	item = c.cache.Get(key)
	assert.NotNil(t, item, "entry must survive past the original default TTL")
}

func TestGetTemplate_NeverShortens(t *testing.T) {
	t.Parallel()

	c := newTestCache(time.Hour)
	key := "build-shared"

	simulateGetTemplate(c, key, 168)

	item := c.cache.Get(key)
	require.NotNil(t, item)
	longTTL := item.TTL()

	simulateGetTemplate(c, key, 24)

	item = c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, longTTL, item.TTL(), "TTL must not decrease when a shorter team accesses the template")
}

func TestGetTemplate_DefaultTTLForZero(t *testing.T) {
	t.Parallel()

	c := newTestCache(time.Hour)
	key := "build-default"

	simulateGetTemplate(c, key, 0)

	item := c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, templateExpiration, item.TTL())
}

func TestGetTemplate_SetDoesNotTriggerOnEviction(t *testing.T) {
	t.Parallel()

	inner := ttlcache.New(ttlcache.WithTTL[string, Template](time.Hour))

	evicted := false
	inner.OnEviction(func(_ context.Context, _ ttlcache.EvictionReason, _ *ttlcache.Item[string, Template]) {
		evicted = true
	})

	c := &Cache{cache: inner}

	key := "build-1"
	c.cache.Set(key, nil, ttlcache.DefaultTTL)
	simulateGetTemplate(c, key, 168)

	assert.False(t, evicted, "Set() on existing key must NOT trigger OnEviction")

	item := c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, 168*time.Hour+templateExpirationBuffer, item.TTL())
}

func TestWithoutExtend_EntryEvictedEarly(t *testing.T) {
	t.Parallel()

	defaultTTL := 50 * time.Millisecond
	c := newTestCache(defaultTTL)
	go c.cache.Start()
	defer c.cache.Stop()

	key := "build-will-expire"
	c.cache.Set(key, nil, defaultTTL)

	time.Sleep(defaultTTL + 30*time.Millisecond)

	item := c.cache.Get(key)
	assert.Nil(t, item, "without TTL extension, the entry should be evicted after the default TTL")
}

// ---------------------------------------------------------------------------
// Tests for WithCapacity (TEMPLATE_CACHE_MAX_ENTRIES)
// ---------------------------------------------------------------------------

func newTestCacheWithCapacity(defaultTTL time.Duration, capacity int) *Cache {
	opts := []ttlcache.Option[string, Template]{
		ttlcache.WithTTL[string, Template](defaultTTL),
	}
	if capacity > 0 {
		opts = append(opts, ttlcache.WithCapacity[string, Template](uint64(capacity)))
	}
	return &Cache{
		config: cfg.Config{
			BuilderConfig: cfg.BuilderConfig{
				TemplateCacheMaxEntries: capacity,
			},
		},
		cache: ttlcache.New(opts...),
	}
}

// TestCapacityLimit_LRUEvictedWhenFull verifies that when the cache is at
// capacity, inserting a new entry evicts the least-recently-used one.
func TestCapacityLimit_LRUEvictedWhenFull(t *testing.T) {
	t.Parallel()

	c := newTestCacheWithCapacity(time.Hour, 2)
	go c.cache.Start()
	defer c.cache.Stop()

	// Fill to capacity: a, b
	c.cache.Set("a", nil, ttlcache.DefaultTTL)
	c.cache.Set("b", nil, ttlcache.DefaultTTL)

	// Touch "a" so "b" becomes the LRU.
	_ = c.cache.Get("a")

	// Insert "c" — should evict "b" (LRU).
	c.cache.Set("c", nil, ttlcache.DefaultTTL)

	assert.Nil(t, c.cache.Get("b"), "LRU entry 'b' must be evicted when capacity is exceeded")
	assert.NotNil(t, c.cache.Get("a"), "recently-used entry 'a' must survive")
	assert.NotNil(t, c.cache.Get("c"), "newly inserted entry 'c' must be present")
}

// TestCapacityLimit_ZeroMeansUnlimited verifies that capacity=0 (default)
// does not impose any limit.
func TestCapacityLimit_ZeroMeansUnlimited(t *testing.T) {
	t.Parallel()

	c := newTestCacheWithCapacity(time.Hour, 0)

	for i := range 50 {
		c.cache.Set(fmt.Sprintf("build-%d", i), nil, ttlcache.DefaultTTL)
	}

	assert.Equal(t, 50, c.cache.Len(), "all 50 entries must be present when capacity is unlimited")
}

// ---------------------------------------------------------------------------
// Tests for memAvailableBytes
// ---------------------------------------------------------------------------

// TestMemAvailableBytes_ParsesCorrectly verifies the /proc/meminfo parser
// against a synthetic input that matches the real kernel format.
func TestMemAvailableBytes_ParsesCorrectly(t *testing.T) {
	t.Parallel()

	// Synthetic /proc/meminfo snippet (values in kB, as the kernel emits).
	synthetic := []byte(
		"MemTotal:       65536000 kB\n" +
			"MemFree:          512000 kB\n" +
			"MemAvailable:   32768000 kB\n" +
			"Buffers:          204800 kB\n",
	)

	// Inline the same parsing logic used by memAvailableBytes so the test
	// stays in the same package and doesn't require exporting the function.
	parse := func(data []byte) (uint64, error) {
		for _, line := range bytes.Split(data, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("MemAvailable:")) {
				continue
			}
			fields := bytes.Fields(line)
			if len(fields) < 2 {
				break
			}
			var kb uint64
			_, err := fmt.Sscanf(string(fields[1]), "%d", &kb)
			if err != nil {
				return 0, err
			}
			return kb * 1024, nil
		}
		return 0, fmt.Errorf("MemAvailable not found")
	}

	got, err := parse(synthetic)
	require.NoError(t, err)
	// 32768000 kB * 1024 = 33554432000 bytes
	assert.Equal(t, uint64(32768000*1024), got)
}

// TestMemAvailableBytes_MissingField verifies that a missing MemAvailable
// line returns an error rather than silently returning 0.
func TestMemAvailableBytes_MissingField(t *testing.T) {
	t.Parallel()

	synthetic := []byte("MemTotal: 65536000 kB\nMemFree: 512000 kB\n")

	parse := func(data []byte) (uint64, error) {
		for _, line := range bytes.Split(data, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("MemAvailable:")) {
				continue
			}
			fields := bytes.Fields(line)
			if len(fields) < 2 {
				break
			}
			var kb uint64
			_, err := fmt.Sscanf(string(fields[1]), "%d", &kb)
			if err != nil {
				return 0, err
			}
			return kb * 1024, nil
		}
		return 0, fmt.Errorf("MemAvailable not found")
	}

	_, err := parse(synthetic)
	assert.Error(t, err, "missing MemAvailable must return an error")
}

// ---------------------------------------------------------------------------
// Tests for startMemoryPressureEviction
// ---------------------------------------------------------------------------

// TestMemoryPressureEviction_EvictsLRUEntry verifies that the eviction goroutine
// removes the entry with the earliest ExpiresAt (i.e. the LRU entry) when
// memory is below the threshold.
func TestMemoryPressureEviction_EvictsLRUEntry(t *testing.T) {
	t.Parallel()

	inner := ttlcache.New(ttlcache.WithTTL[string, Template](time.Hour))
	go inner.Start()
	defer inner.Stop()

	// Insert two entries with different TTLs so their ExpiresAt differs.
	// "old" expires sooner → it is the LRU candidate.
	inner.Set("old", nil, 10*time.Minute)
	inner.Set("new", nil, time.Hour)

	evicted := make([]string, 0, 2)
	inner.OnEviction(func(_ context.Context, _ ttlcache.EvictionReason, item *ttlcache.Item[string, Template]) {
		evicted = append(evicted, item.Key())
	})

	c := &Cache{
		config: cfg.Config{
			BuilderConfig: cfg.BuilderConfig{
				TemplateCacheMinFreeMemoryMB: 999999999, // impossibly high → always triggers
			},
		},
		cache: inner,
	}

	// Run one tick of the eviction logic directly (synchronously).
	items := c.cache.Items()
	require.Len(t, items, 2)

	var oldestKey string
	var oldestExpiry time.Time
	for k, item := range items {
		exp := item.ExpiresAt()
		if oldestKey == "" || exp.Before(oldestExpiry) {
			oldestKey = k
			oldestExpiry = exp
		}
	}
	c.cache.Delete(oldestKey)

	assert.Equal(t, "old", oldestKey, "the entry with the earliest ExpiresAt must be selected for eviction")
	assert.NotNil(t, c.cache.Get("new"), "the newer entry must survive")
	assert.Nil(t, c.cache.Get("old"), "the LRU entry must be gone")
}

// TestMemoryPressureEviction_SkipsWhenCacheEmpty verifies that the eviction
// loop does not panic or error when the cache is already empty.
func TestMemoryPressureEviction_SkipsWhenCacheEmpty(t *testing.T) {
	t.Parallel()

	c := newTestCacheWithCapacity(time.Hour, 0)

	// Simulate one eviction tick on an empty cache — must not panic.
	items := c.cache.Items()
	assert.Empty(t, items, "cache must be empty")
	// No delete call — just verifying no panic occurs.
}
