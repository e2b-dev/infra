//go:build linux

package template

import (
	"context"
	"testing"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestSelectEvictions(t *testing.T) {
	t.Parallel()

	now := time.Now()
	stale := now.Add(-time.Hour)   // outside grace window
	fresh := now.Add(-time.Second) // inside grace window
	mib := int64(1) << 20

	entry := func(key string, fpMiB int64, exp, last time.Time) evictionEntry {
		return evictionEntry{key: key, footprint: fpMiB * mib, expiresAt: exp, lastAccess: last}
	}

	t.Run("under budget evicts nothing", func(t *testing.T) {
		t.Parallel()
		entries := []evictionEntry{entry("a", 5, now.Add(time.Hour), stale)}
		got := selectEvictions(entries, nil, now, 10*mib, 5*mib)
		assert.Empty(t, got)
	})

	t.Run("evicts oldest-first until under budget", func(t *testing.T) {
		t.Parallel()
		entries := []evictionEntry{
			entry("new", 10, now.Add(3*time.Hour), stale),
			entry("old", 10, now.Add(time.Hour), stale),
			entry("mid", 10, now.Add(2*time.Hour), stale),
		}
		// total 30 MiB, budget 15 MiB -> must free >=15, evicting 2 oldest.
		got := selectEvictions(entries, nil, now, 15*mib, 30*mib)
		assert.Equal(t, []string{"old", "mid"}, got)
	})

	t.Run("never evicts in-use templates", func(t *testing.T) {
		t.Parallel()
		entries := []evictionEntry{
			entry("running", 100, now.Add(time.Hour), stale),
			entry("idle", 10, now.Add(2*time.Hour), stale),
		}
		active := map[string]struct{}{"running": {}}
		got := selectEvictions(entries, active, now, 5*mib, 110*mib)
		// running is skipped even though it is the largest and oldest; only idle
		// is eligible (and still over budget, but nothing else can be freed).
		assert.Equal(t, []string{"idle"}, got)
	})

	t.Run("never evicts within grace window", func(t *testing.T) {
		t.Parallel()
		entries := []evictionEntry{
			entry("starting", 100, now.Add(time.Hour), fresh),
			entry("idle", 10, now.Add(2*time.Hour), stale),
		}
		got := selectEvictions(entries, nil, now, 5*mib, 110*mib)
		assert.Equal(t, []string{"idle"}, got)
	})

	t.Run("never evicts entries with no recorded access", func(t *testing.T) {
		t.Parallel()
		entries := []evictionEntry{entry("unknown", 100, now.Add(time.Hour), time.Time{})}
		got := selectEvictions(entries, nil, now, 5*mib, 100*mib)
		assert.Empty(t, got)
	})

	t.Run("never evicts zero-footprint entries", func(t *testing.T) {
		t.Parallel()
		entries := []evictionEntry{
			entry("fetching", 0, now.Add(time.Hour), stale),
			entry("idle", 10, now.Add(2*time.Hour), stale),
		}
		got := selectEvictions(entries, nil, now, 5*mib, 10*mib)
		assert.Equal(t, []string{"idle"}, got)
	})
}
