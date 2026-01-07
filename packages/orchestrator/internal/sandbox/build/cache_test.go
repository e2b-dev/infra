package build

// Race Condition Tests:
// To reproduce the data race condition reported in the cache eviction callbacks,
// run the following tests with the race detector enabled:
//
// Run all race tests:    go test -race -v -run "TestDiffStore.*Race"
// Run first race test:   go test -race -v -run TestDiffStoreConcurrentEvictionRace
// Run second race test:  go test -race -v -run TestDiffStoreResetDeleteRace
//
// These tests simulate the race condition where multiple OnEviction callbacks
// run concurrently and both try to access the same key in the resetDelete method,
// causing a race when closing the cancel channel.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

const (
	blockSize = int64(1024)
)

func newDiff(t *testing.T, cachePath, buildId string, diffType DiffType, blockSize int64) Diff {
	t.Helper()

	localDiff, err := NewLocalDiffFile(cachePath, buildId, diffType)
	require.NoError(t, err)

	// Write 100 bytes to the file
	n, err := localDiff.WriteAt(make([]byte, 100), 0)
	require.NoError(t, err)
	assert.Equal(t, 100, n)

	diff, err := localDiff.CloseToDiff(blockSize)
	require.NoError(t, err)

	return diff
}

func newDiffWithAsserts(t *testing.T, cachePath, buildId string, diffType DiffType, blockSize int64) (Diff, error) {
	t.Helper()

	localDiff, err := NewLocalDiffFile(cachePath, buildId, diffType)
	if err != nil {
		return nil, err
	}

	// Write 100 bytes to the file
	n, err := localDiff.WriteAt(make([]byte, 100), 0)
	if err != nil {
		return nil, err
	}
	assert.Equal(t, 100, n)

	diff, err := localDiff.CloseToDiff(blockSize)
	if err != nil {
		return nil, err
	}

	return diff, nil
}

func TestNewDiffStore(t *testing.T) {
	t.Parallel()
	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	flags := flagsWithMaxBuildCachePercentage(t, 90)

	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		25*time.Hour,
		60*time.Second,
	)
	require.NoError(t, err)
	assert.NotNil(t, store)
}

func TestDiffStoreTTLEviction(t *testing.T) {
	t.Parallel()
	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	flags := flagsWithMaxBuildCachePercentage(t, 100)

	ttl := 1 * time.Second
	delay := 60 * time.Second
	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		ttl,
		delay,
	)
	require.NoError(t, err)

	store.Start(t.Context())
	t.Cleanup(store.Close)

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
	t.Parallel()
	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	flags := flagsWithMaxBuildCachePercentage(t, 100)

	ttl := 1 * time.Second
	delay := 60 * time.Second
	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		ttl,
		delay,
	)
	require.NoError(t, err)

	// Add an item to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

	// Add an item to the cache
	store.Add(diff)

	// Refresh diff expiration
	time.Sleep(ttl / 2)
	_, err = store.Get(t.Context(), diff)
	require.NoError(t, err)

	// Try to expire diff
	time.Sleep(ttl/2 + time.Microsecond)

	// Is still in cache
	found2 := store.Has(diff)
	assert.True(t, found2)
}

func TestDiffStoreDelayEviction(t *testing.T) { //nolint:paralleltest // very timing sensitive
	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	flags := flagsWithMaxBuildCachePercentage(t, 0)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		ttl,
		delay,
	)
	require.NoError(t, err)

	store.Start(t.Context())
	t.Cleanup(store.Close)

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

func TestDiffStoreDelayEvictionAbort(t *testing.T) { //nolint:paralleltest // very timing sensitive
	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	flags := flagsWithMaxBuildCachePercentage(t, 0)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		ttl,
		delay,
	)
	require.NoError(t, err)

	store.Start(t.Context())
	t.Cleanup(store.Close)

	// Add an item to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)

	// Add an item to the cache
	store.Add(diff)

	// Wait for removal trigger of diff
	time.Sleep(delay / 2)

	// Verify still in cache
	found := store.Has(diff)
	assert.True(t, found)
	dFound := store.isBeingDeleted(diff.CacheKey())
	assert.True(t, dFound)

	// Abort removal of diff
	_, err = store.Get(t.Context(), diff)
	require.NoError(t, err)

	found = store.Has(diff)
	assert.True(t, found)
	dFound = store.isBeingDeleted(diff.CacheKey())
	assert.False(t, dFound)

	// Check insufficient delay cancellation of diff and verify it's still in the cache
	// after the delay period
	time.Sleep(delay/2 + time.Second)
	found = store.Has(diff)
	assert.True(t, found)
}

func TestDiffStoreOldestFromCache(t *testing.T) {
	t.Parallel()
	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	flags := flagsWithMaxBuildCachePercentage(t, 100)

	ttl := 60 * time.Second
	delay := 4 * time.Second
	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		ttl,
		delay,
	)
	require.NoError(t, err)

	// Add items to the cache
	diff := newDiff(t, cachePath, "build-test-id", Rootfs, blockSize)
	store.Add(diff)
	diff2 := newDiff(t, cachePath, "build-test-id-2", Rootfs, blockSize)
	store.Add(diff2)

	found := store.Has(diff)
	assert.True(t, found)

	// Delete oldest item
	_, err = store.deleteOldestFromCache(t.Context())
	require.NoError(t, err)

	assert.True(t, store.isBeingDeleted(diff.CacheKey()))
	// Wait for removal trigger of diff
	time.Sleep(delay + time.Second)

	// Verify oldest item is deleted
	found = store.Has(diff)
	assert.False(t, found)

	found = store.Has(diff2)
	assert.True(t, found)

	// Add another item to the cache
	diff3 := newDiff(t, cachePath, "build-test-id-3", Rootfs, blockSize)
	store.Add(diff3)

	// Delete oldest item
	_, err = store.deleteOldestFromCache(t.Context())
	require.NoError(t, err)

	assert.True(t, store.isBeingDeleted(diff2.CacheKey()))
	// Wait for removal trigger of diff
	time.Sleep(delay + time.Second)

	// Verify oldest item is deleted
	found = store.Has(diff2)
	assert.False(t, found)

	found = store.Has(diff3)
	assert.True(t, found)
}

// TestDiffStoreConcurrentEvictionRace simulates the data race condition where
// multiple eviction callbacks run concurrently and both try to close the same
// cancel channel in resetDelete method. This test should be run with the race
// detector enabled: go test -race
func TestDiffStoreConcurrentEvictionRace(t *testing.T) {
	t.Parallel()

	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	// Set to 0% to trigger disk space evictions
	flags := flagsWithMaxBuildCachePercentage(t, 0)

	// Use very short TTL and delay to trigger rapid evictions
	ttl := 10 * time.Millisecond
	delay := 50 * time.Millisecond
	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		ttl,
		delay,
	)
	require.NoError(t, err)

	store.Start(t.Context())
	t.Cleanup(store.Close)

	// Number of concurrent operations to create race conditions
	numGoroutines := 50
	numIterations := 100

	var wg sync.WaitGroup

	// Create multiple goroutines that add and remove items rapidly
	for i := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := range numIterations {
				// Create diffs with same buildID but different iterations
				// This increases chances of race conditions
				buildID := fmt.Sprintf("build-%d", goroutineID%10) // Limit to 10 different build IDs
				diff, err := newDiffWithAsserts(t, cachePath, buildID, Rootfs, blockSize)
				if !assert.NoError(t, err) {
					continue
				}

				// Add to store
				store.Add(diff)

				// Small delay to allow TTL expiration and concurrent access
				time.Sleep(time.Microsecond * 100)

				// Try to trigger manual deletion which can race with TTL eviction
				if j%10 == 0 {
					_, err := store.deleteOldestFromCache(t.Context())
					assert.NoError(t, err)
				}

				// Occasionally try to access the item, which calls resetDelete
				if j%5 == 0 {
					_, err := store.Get(t.Context(), diff)
					assert.NoError(t, err)
				}
			}
		}(i)
	}

	// Additional goroutine that continuously tries to delete oldest items
	// to increase race condition probability
	wg.Go(func() {
		for range numIterations * 2 {
			_, err = store.deleteOldestFromCache(t.Context())
			assert.NoError(t, err) //nolint:testifylint
			time.Sleep(time.Microsecond * 50)
		}
	})

	// Wait for all goroutines to complete
	wg.Wait()

	// Allow some time for pending deletions to complete
	time.Sleep(delay * 2)

	// Test passes if no race condition panic occurs
	// The race detector will catch the race if it occurs
}

// TestDiffStoreResetDeleteRace specifically targets the resetDelete method
// race condition by simulating the exact scenario from the race report
func TestDiffStoreResetDeleteRace(t *testing.T) {
	t.Parallel()

	cachePath := t.TempDir()

	c, err := cfg.Parse()
	require.NoError(t, err)

	flags := flagsWithMaxBuildCachePercentage(t, 100)

	// Very short TTL to trigger evictions quickly
	ttl := 5 * time.Millisecond
	delay := 100 * time.Millisecond
	store, err := NewDiffStore(
		c,
		flags,
		cachePath,
		ttl,
		delay,
	)
	require.NoError(t, err)

	store.Start(t.Context())
	t.Cleanup(store.Close)

	// Create a base build ID for generating test diffs
	buildID := "race-test-build"

	var wg sync.WaitGroup
	const numConcurrentOps = 100

	// Simulate the exact race condition:
	// 1. Add item to cache
	// 2. Schedule it for deletion (creates entry in pdSizes)
	// 3. Multiple goroutines try to reset the deletion simultaneously

	for i := range numConcurrentOps {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()

			// Create a unique diff for this iteration to increase concurrency
			iterDiff, err := newDiffWithAsserts(t, cachePath, fmt.Sprintf("%s-%d", buildID, iteration), Rootfs, blockSize)
			if !assert.NoError(t, err) {
				return
			}

			// Add to store
			store.Add(iterDiff)

			// Immediately schedule for deletion to populate pdSizes
			store.scheduleDelete(t.Context(), iterDiff.CacheKey(), 1024)

			// Small random delay to desynchronize goroutines slightly
			time.Sleep(time.Duration(iteration%10) * time.Microsecond)

			// This call to Get() will trigger resetDelete, which is where the race occurs
			// Multiple goroutines calling resetDelete on the same key can race
			_, err = store.Get(t.Context(), iterDiff)
			assert.NoError(t, err)

			// Also try direct resetDelete calls to increase race probability
			store.resetDelete(iterDiff.CacheKey())
		}(i)
	}

	// Wait for all goroutines
	wg.Wait()

	// Allow cleanup to complete
	time.Sleep(delay * 2)
}

func flagsWithMaxBuildCachePercentage(tb testing.TB, maxBuildCachePercentage int) *featureflags.Client {
	tb.Helper()

	datastore := ldtestdata.DataSource()

	datastore.Update(
		datastore.Flag(featureflags.BuildCacheMaxUsagePercentage.String()).
			ValueForAll(ldvalue.Int(maxBuildCachePercentage)),
	)

	flags, err := featureflags.NewClientWithDatasource(datastore)
	require.NoError(tb, err)

	tb.Cleanup(func() {
		err := flags.Close(tb.Context())
		assert.NoError(tb, err)
	})

	return flags
}
