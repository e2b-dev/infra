package build

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

const tmpBuildCachePrefix = "test-build-cache_"

func createTempDir(t *testing.T) string {
	tempDir, err := os.MkdirTemp("", tmpBuildCachePrefix)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})
	return tempDir
}

func TestNewDiffStore(t *testing.T) {
	cachePath := createTempDir(t)
	ctx := context.Background()

	store, err := NewDiffStore(
		gcs.TemplateBucket,
		ctx,
		cachePath,
		25*time.Hour,
		60*time.Second,
		100.0,
	)
	assert.NoError(t, err)
	assert.NotNil(t, store)
}

func TestCacheEviction(t *testing.T) {
	cachePath := createTempDir(t)
	ctx := context.Background()

	ttl := 1 * time.Second
	cache, err := NewDiffStore(
		gcs.TemplateBucket,
		ctx,
		cachePath,
		ttl,
		60*time.Second,
		100.0,
	)
	assert.NoError(t, err)

	// Add an item to the cache
	blockSize := int64(1024)
	buildId := "build-test-id"
	diff1 := newStorageDiff(cachePath, buildId, Rootfs, blockSize)
	diff2 := newStorageDiff(cachePath, buildId, Memfile, blockSize)

	// Add an item to the cache
	cache.Add(buildId, Rootfs, diff1)
	cache.Add(buildId, Memfile, diff2)

	time.Sleep(ttl / 2)
	_, err = cache.Get(buildId, Memfile, blockSize)
	assert.NoError(t, err)

	// Expire diff1
	time.Sleep(ttl / 2)

	found1 := cache.cache.Has(diff1.CacheKey())
	assert.False(t, found1)

	found2 := cache.cache.Has(diff2.CacheKey())
	assert.True(t, found2)
}
