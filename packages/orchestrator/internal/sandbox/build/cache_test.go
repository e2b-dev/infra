package build

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	tmpBuildCachePrefix = "test-build-cache_"

	blockSize = int64(1024)
)

func newDiff(t *testing.T, cachePath, buildId string, diffType DiffType, blockSize int64) Diff {
	localDiff, err := NewLocalDiffFile(cachePath, buildId, diffType)
	assert.NoError(t, err)

	// Write 100 bytes to the file
	n, err := localDiff.WriteAt(make([]byte, 100), 0)
	assert.NoError(t, err)
	assert.Equal(t, 100, n)

	diff, err := localDiff.ToDiff(blockSize)
	assert.NoError(t, err)

	return diff
}

func createTempDir(t *testing.T) string {
	tempDir, err := os.MkdirTemp("", tmpBuildCachePrefix)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	t.Logf("Temp dir: %s\n", tempDir)
	return tempDir
}

func TestNewDiffStore(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store, err := NewDiffStore(
		ctx,
		cachePath,
		25*time.Hour,
		60*time.Second,
		90.0,
	)
	t.Cleanup(store.Close)

	assert.NoError(t, err)
	assert.NotNil(t, store)
}

func TestDiffStoreTTLEviction(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 1 * time.Second
	delay := 60 * time.Second
	store, err := NewDiffStore(
		ctx,
		cachePath,
		ttl,
		delay,
		100.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

	// Add an item to the cache
	store.Add(diff)

	// Expire diff
	time.Sleep(ttl + time.Second)

	found := store.Has(diff)
	assert.False(t, found)
}

func TestDiffStoreRefreshTTLEviction(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 1 * time.Second
	delay := 60 * time.Second
	store, err := NewDiffStore(
		ctx,
		cachePath,
		ttl,
		delay,
		100.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

	// Add an item to the cache
	store.Add(diff)

	// Refresh diff expiration
	time.Sleep(ttl / 2)
	_, err = store.Get(diff)
	assert.NoError(t, err)

	// Try to expire diff
	time.Sleep(ttl/2 + time.Microsecond)

	// Is still in cache
	found2 := store.Has(diff)
	assert.True(t, found2)
}

func TestDiffStoreDelayEviction(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		ctx,
		cachePath,
		ttl,
		delay,
		0.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

	// Add an item to the cache
	store.Add(diff)

	// Wait for removal trigger of diff
	time.Sleep(2 * time.Second)

	// Verify still in cache
	found := store.Has(diff)
	assert.True(t, found)
	dFound := store.isBeingDeleted(diff.CacheKey())
	assert.True(t, dFound)

	// Wait for complete removal of diff
	time.Sleep(delay)

	found = store.Has(diff)
	assert.False(t, found)
	dFound = store.isBeingDeleted(diff.CacheKey())
	assert.False(t, dFound)
}

func TestDiffStoreDelayEvictionAbort(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		ctx,
		cachePath,
		ttl,
		delay,
		0.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

	// Add an item to the cache
	store.Add(diff)

	// Wait for removal trigger of diff
	time.Sleep(2 * time.Second)

	// Verify still in cache
	found := store.Has(diff)
	assert.True(t, found)
	dFound := store.isBeingDeleted(diff.CacheKey())
	assert.True(t, dFound)

	// Abort removal of diff
	_, err = store.Get(diff)
	assert.NoError(t, err)

	found = store.Has(diff)
	assert.True(t, found)
	dFound = store.isBeingDeleted(diff.CacheKey())
	assert.False(t, dFound)

	// Check insufficient delay cancellation of diff and verify it's still in the cache
	// after the delay period
	time.Sleep(delay)
	found = store.Has(diff)
	assert.True(t, found)
}

func TestDiffStoreOldestFromCache(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		ctx,
		cachePath,
		ttl,
		delay,
		100.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add items to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)
	store.Add(diff)
	diff2 := newDiff(t, cachePath, "build-test-id-2", Rootfs, blockSize)
	store.Add(diff2)

	found := store.Has(diff)
	assert.True(t, found)

	// Delete oldest item
	store.deleteOldestFromCache()

	assert.True(t, store.isBeingDeleted(diff.CacheKey()))
	// Wait for removal trigger of diff
	time.Sleep(delay + time.Microsecond)

	// Verify oldest item is deleted
	found = store.Has(diff)
	assert.False(t, found)

	found = store.Has(diff2)
	assert.True(t, found)

	// Add another item to the cache
	diff3 := newDiff(t, cachePath, "build-test-id-3", Rootfs, blockSize)
	store.Add(diff3)

	// Delete oldest item
	store.deleteOldestFromCache()

	assert.True(t, store.isBeingDeleted(diff2.CacheKey()))
	// Wait for removal trigger of diff
	time.Sleep(delay + time.Microsecond)

	// Verify oldest item is deleted
	found = store.Has(diff2)
	assert.False(t, found)

	found = store.Has(diff3)
	assert.True(t, found)
}
