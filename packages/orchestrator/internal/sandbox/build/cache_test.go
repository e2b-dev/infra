package build

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

const (
	tmpBuildCachePrefix = "test-build-cache_"

	blockSize = int64(1024)
)

type mockDiff struct {
	cachePath string
	buildId   string
	diffType  DiffType
	blockSize int64
}

func newMockDiff(cachePath, buildId string, diffType DiffType, blockSize int64) *mockDiff {
	return &mockDiff{
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
	return nil
}

func (m *mockDiff) CacheKey() string {
	return storagePath(m.buildId, m.diffType)
}

func createTempDir(t *testing.T) string {
	tempDir, err := os.MkdirTemp("", tmpBuildCachePrefix)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	fmt.Printf("Temp dir: %s\n", tempDir)
	return tempDir
}

func TestNewDiffStore(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store, err := NewDiffStore(
		gcs.TemplateBucket,
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
		gcs.TemplateBucket,
		ctx,
		cachePath,
		ttl,
		delay,
		100.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	buildId := "build-test-id"
	diff1 := newMockDiff(cachePath, buildId, Rootfs, blockSize)

	// Add an item to the cache
	store.Add(buildId, Rootfs, diff1)

	// Expire diff1
	time.Sleep(ttl + time.Second)

	found1 := store.Has(buildId, Rootfs)
	assert.False(t, found1)
}

func TestDiffStoreRefreshTTLEviction(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 1 * time.Second
	delay := 60 * time.Second
	store, err := NewDiffStore(
		gcs.TemplateBucket,
		ctx,
		cachePath,
		ttl,
		delay,
		100.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	buildId := "build-test-id"
	diffType := Rootfs
	diff1 := newMockDiff(cachePath, buildId, diffType, blockSize)

	// Add an item to the cache
	store.Add(buildId, diffType, diff1)

	// Refresh diff1 expiration
	time.Sleep(ttl / 2)
	_, err = store.Get(buildId, diffType, blockSize)
	assert.NoError(t, err)

	// Try to expire diff1
	time.Sleep(ttl/2 + time.Microsecond)

	// Is still in cache
	found2 := store.Has(buildId, diffType)
	assert.True(t, found2)
}

func TestDiffStoreDelayEviction(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		gcs.TemplateBucket,
		ctx,
		cachePath,
		ttl,
		delay,
		0.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	buildId := "build-test-id"
	diffType := Rootfs
	diff1 := newMockDiff(cachePath, buildId, diffType, blockSize)

	// Add an item to the cache
	store.Add(buildId, diffType, diff1)

	// Wait for removal trigger of diff1
	time.Sleep(2 * time.Second)

	// Verify still in cache
	found1 := store.Has(buildId, diffType)
	assert.True(t, found1)
	dFound1 := store.isBeingDeleted(diff1.CacheKey())
	assert.True(t, dFound1)

	// Wait for complete removal of diff1
	time.Sleep(delay)

	found1 = store.Has(buildId, diffType)
	assert.False(t, found1)
	dFound1 = store.isBeingDeleted(diff1.CacheKey())
	assert.False(t, dFound1)
}

func TestDiffStoreDelayEvictionAbort(t *testing.T) {
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		gcs.TemplateBucket,
		ctx,
		cachePath,
		ttl,
		delay,
		0.0,
	)
	t.Cleanup(store.Close)
	assert.NoError(t, err)

	// Add an item to the cache
	buildId := "build-test-id"
	diffType := Rootfs
	diff1 := newMockDiff(cachePath, buildId, diffType, blockSize)

	// Add an item to the cache
	store.Add(buildId, diffType, diff1)

	// Wait for removal trigger of diff1
	time.Sleep(2 * time.Second)

	// Verify still in cache
	found1 := store.Has(buildId, diffType)
	assert.True(t, found1)
	dFound1 := store.isBeingDeleted(diff1.CacheKey())
	assert.True(t, dFound1)

	// Abort removal of diff1
	_, err = store.Get(buildId, diffType, blockSize)
	assert.NoError(t, err)

	found1 = store.Has(buildId, diffType)
	assert.True(t, found1)
	dFound1 = store.isBeingDeleted(diff1.CacheKey())
	assert.False(t, dFound1)

	// Check insufficient delay cancellation of diff1 and verify it's still in the cache
	// after the delay period
	time.Sleep(delay)
	found1 = store.Has(buildId, diffType)
	assert.True(t, found1)
}
