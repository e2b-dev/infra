package userfaultfd

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNoOperations(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(512)

	h, err := configureCrossProcessTest(t, testConfig{
		pagesize:      pagesize,
		numberOfPages: numberOfPages,
	})
	require.NoError(t, err)

	// Placeholder uffd that does not serve anything
	uffd, err := NewUserfaultfdFromFd(uintptr(h.uffd), h.data, &memory.Mapping{}, zap.L())
	require.NoError(t, err)

	accessedOffsets, err := h.accessedOffsetsOnce()
	require.NoError(t, err)

	assert.Empty(t, accessedOffsets, "checking which pages were faulted")

	dirtyOffsets, err := h.dirtyOffsetsOnce()
	require.NoError(t, err)

	assert.Empty(t, dirtyOffsets, "checking which pages were dirty")

	dirty := uffd.Dirty(false)
	assert.Empty(t, slices.Collect(dirty.Offsets()), "checking dirty pages")
}

// We are using internals from the uffd here, because the cross process helpers make calling these methods very complicated.
func TestNoDirtyOperationsAfterDirtyReset(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(32)

	h, err := configureCrossProcessTest(t, testConfig{
		pagesize:      pagesize,
		numberOfPages: numberOfPages,
	})
	require.NoError(t, err)

	// Placeholder uffd that does not serve anything
	uffd, err := NewUserfaultfdFromFd(uintptr(h.uffd), h.data, &memory.Mapping{}, zap.L())
	require.NoError(t, err)

	uffd.writeRequests.Add(0)
	uffd.writeRequests.Add(1 * header.PageSize)
	uffd.missingRequests.Add(0)

	d1 := uffd.Dirty(true)
	assert.ElementsMatch(t, []int64{0, 1 * header.PageSize}, slices.Collect(d1.Offsets()), "checking dirty pages")

	d2 := uffd.Dirty(true)
	assert.ElementsMatch(t, []int64{}, slices.Collect(d2.Offsets()), "checking dirty pages after reset")
}

// We are using internals from the uffd here, because the cross process helpers make calling these methods very complicated.
func TestDirtyOperationsAfterDirtyNoReset(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(32)

	h, err := configureCrossProcessTest(t, testConfig{
		pagesize:      pagesize,
		numberOfPages: numberOfPages,
	})
	require.NoError(t, err)

	// Placeholder uffd that does not serve anything
	uffd, err := NewUserfaultfdFromFd(uintptr(h.uffd), h.data, &memory.Mapping{}, zap.L())
	require.NoError(t, err)

	uffd.writeRequests.Add(0)
	uffd.writeRequests.Add(1 * header.PageSize)
	uffd.missingRequests.Add(0)

	d1 := uffd.Dirty(false)
	assert.ElementsMatch(t, []int64{0, 1 * header.PageSize}, slices.Collect(d1.Offsets()), "checking dirty pages")

	d2 := uffd.Dirty(false)
	assert.ElementsMatch(t, []int64{0, 1 * header.PageSize}, slices.Collect(d2.Offsets()), "checking dirty pages without reset")
}

// We are using internals from the uffd here, because the cross process helpers make calling these methods very complicated.
func TestSettleRequestLocking(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(32)

	h, err := configureCrossProcessTest(t, testConfig{
		pagesize:      pagesize,
		numberOfPages: numberOfPages,
	})
	require.NoError(t, err)

	uffd, err := NewUserfaultfdFromFd(uintptr(h.uffd), h.data, &memory.Mapping{}, zap.L())
	require.NoError(t, err)

	uffd.writeRequests.Add(0)
	uffd.writeRequests.Add(1 * header.PageSize)
	uffd.missingRequests.Add(0)

	uffd.settleRequests.RLock()
	uffd.settleRequests.RLock()
	uffd.settleRequests.RLock()
	uffd.settleRequests.RLock()

	r := make(chan *block.Tracker, 1)
	go func() {
		select {
		case r <- uffd.Dirty(false):
		case <-t.Context().Done():
			return
		}
	}()

	success := uffd.settleRequests.TryLock()
	assert.False(t, success, "settleRequests write lock should not be acquired")

	success = uffd.settleRequests.TryLock()
	assert.False(t, success, "settleRequests write lock should not be acquired")

	uffd.settleRequests.RUnlock()
	uffd.settleRequests.RUnlock()
	uffd.settleRequests.RUnlock()

	success = uffd.settleRequests.TryLock()
	assert.False(t, success, "settleRequests write lock should still not be acquired")

	uffd.settleRequests.RUnlock()

	// This should not get blocked as the Dirty should release the lock after returning.
	uffd.settleRequests.Lock()
	// Unlock so we are sure the goroutine is not blocked.
	uffd.settleRequests.Unlock() //nolint:staticcheck

	select {
	case result, ok := <-r:
		if !ok {
			t.FailNow()
		}

		assert.ElementsMatch(t, []int64{0, 1 * header.PageSize}, slices.Collect(result.Offsets()), "checking dirty pages")
	case <-t.Context().Done():
	}
}

func TestRandomPagesOperations(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(4096)
	numberOfOperations := 2048
	repetitions := 8

	for i := range repetitions {
		t.Run(fmt.Sprintf("Run_%d_of_%d", i+1, repetitions-1), func(t *testing.T) {
			t.Parallel()

			// Use time-based seed for each run to ensure different random sequences
			// This increases the chance of catching bugs that only manifest with specific sequences
			seed := time.Now().UnixNano() + int64(i)
			rng := rand.New(rand.NewSource(seed))

			t.Logf("Using random seed: %d", seed)

			// Randomly operations on the data
			operations := make([]operation, 0, numberOfOperations)
			for range numberOfOperations {
				operations = append(operations, operation{
					offset: int64(rng.Intn(int(numberOfPages-1)) * int(pagesize)),
					mode:   operationMode(rng.Intn(2) + 1),
				})
			}

			h, err := configureCrossProcessTest(t, testConfig{
				pagesize:      pagesize,
				numberOfPages: numberOfPages,
				operations:    operations,
			})
			require.NoError(t, err)

			for _, operation := range operations {
				switch operation.mode {
				case operationModeRead:
					err := h.executeRead(t.Context(), operation)
					require.NoError(t, err)
				case operationModeWrite:
					err := h.executeWrite(t.Context(), operation)
					require.NoError(t, err)
				default:
					t.FailNow()
				}
			}

			expectedAccessedOffsets := getOperationsOffsets(operations, operationModeRead|operationModeWrite)

			accessedOffsets, err := h.accessedOffsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted (seed: %d)", seed)

			expectedDirtyOffsets := getOperationsOffsets(operations, operationModeWrite)
			dirtyOffsets, err := h.dirtyOffsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedDirtyOffsets, dirtyOffsets, "checking which pages were dirty (seed: %d)", seed)
		})
	}
}
