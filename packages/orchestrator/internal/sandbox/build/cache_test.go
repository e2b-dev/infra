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

type mockDiff struct {
	t         *testing.T
	cachePath string
	buildId   string
	diffType  DiffType
	blockSize int64
}

func newMockDiff(t *testing.T, cachePath, buildId string, diffType DiffType, blockSize int64) *mockDiff {
	return &mockDiff{
		t:         t,
		cachePath: cachePath,
		buildId:   buildId,
		diffType:  diffType,
		blockSize: blockSize,
	}
}

func (m *mockDiff) ReadAt(p []byte, off int64) (n int, err error) {
	return 0, nil
}

func (m *mockDiff) CachePath() (string, error) {
	return m.cachePath, nil
}

func (m *mockDiff) FileSize() (int64, error) {
	return 1024, nil
}

func (m *mockDiff) Slice(off, length int64) ([]byte, error) {
	return nil, nil
}

func (m *mockDiff) Close() error {
	m.t.Logf("Closing diff: %s\n", m.CacheKey())
	return nil
}

func (m *mockDiff) CacheKey() string {
	return storagePath(m.buildId, m.diffType)
}

func (m *mockDiff) Init(ctx context.Context) error {
	return nil
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
	diff := newMockDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

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
	diff := newMockDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

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
	diff := newMockDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

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
	diff := newMockDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

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
