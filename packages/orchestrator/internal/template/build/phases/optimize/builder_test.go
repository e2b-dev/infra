package optimize

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

func TestComputeCommonPrefetchEntries(t *testing.T) {
	t.Parallel()

	t.Run("empty input returns nil", func(t *testing.T) {
		t.Parallel()
		result := computeCommonPrefetchEntries(nil)
		assert.Nil(t, result)

		result = computeCommonPrefetchEntries([]block.PrefetchData{})
		assert.Nil(t, result)
	})

	t.Run("single run returns all entries", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
					2: {Index: 2, Order: 20, AccessType: block.Write},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		assert.Len(t, result, 2)
		// Convert to map for easier assertion
		resultMap := make(map[uint64]block.PrefetchBlockEntry)
		for _, e := range result {
			resultMap[e.Index] = e
		}
		assert.Equal(t, uint64(10), resultMap[1].Order)
		assert.Equal(t, block.Read, resultMap[1].AccessType)
		assert.Equal(t, uint64(20), resultMap[2].Order)
		assert.Equal(t, block.Write, resultMap[2].AccessType)
	})

	t.Run("two runs with complete overlap", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
					2: {Index: 2, Order: 20, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 12, AccessType: block.Read},
					2: {Index: 2, Order: 24, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		assert.Len(t, result, 2)
		resultMap := make(map[uint64]block.PrefetchBlockEntry)
		for _, e := range result {
			resultMap[e.Index] = e
		}
		// Average order: (10+12)/2 = 11, (20+24)/2 = 22
		assert.Equal(t, uint64(11), resultMap[1].Order)
		assert.Equal(t, uint64(22), resultMap[2].Order)
	})

	t.Run("two runs with partial overlap", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
					2: {Index: 2, Order: 20, AccessType: block.Read},
					3: {Index: 3, Order: 30, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					2: {Index: 2, Order: 22, AccessType: block.Read},
					3: {Index: 3, Order: 32, AccessType: block.Read},
					4: {Index: 4, Order: 40, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		// Only indices 2 and 3 are common
		assert.Len(t, result, 2)
		resultMap := make(map[uint64]block.PrefetchBlockEntry)
		for _, e := range result {
			resultMap[e.Index] = e
		}
		assert.Contains(t, resultMap, uint64(2))
		assert.Contains(t, resultMap, uint64(3))
		assert.NotContains(t, resultMap, uint64(1))
		assert.NotContains(t, resultMap, uint64(4))
		// Average order: (20+22)/2 = 21, (30+32)/2 = 31
		assert.Equal(t, uint64(21), resultMap[2].Order)
		assert.Equal(t, uint64(31), resultMap[3].Order)
	})

	t.Run("two runs with no overlap", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
					2: {Index: 2, Order: 20, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					3: {Index: 3, Order: 30, AccessType: block.Read},
					4: {Index: 4, Order: 40, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		assert.Empty(t, result)
	})

	t.Run("different access types prefers read", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Write},
					2: {Index: 2, Order: 20, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 12, AccessType: block.Read},
					2: {Index: 2, Order: 22, AccessType: block.Write},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		assert.Len(t, result, 2)
		resultMap := make(map[uint64]block.PrefetchBlockEntry)
		for _, e := range result {
			resultMap[e.Index] = e
		}
		// Both should be read since they differ
		assert.Equal(t, block.Read, resultMap[1].AccessType)
		assert.Equal(t, block.Read, resultMap[2].AccessType)
	})

	t.Run("same access types preserved", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Write},
					2: {Index: 2, Order: 20, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 12, AccessType: block.Write},
					2: {Index: 2, Order: 22, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		assert.Len(t, result, 2)
		resultMap := make(map[uint64]block.PrefetchBlockEntry)
		for _, e := range result {
			resultMap[e.Index] = e
		}
		// Same access types should be preserved
		assert.Equal(t, block.Write, resultMap[1].AccessType)
		assert.Equal(t, block.Read, resultMap[2].AccessType)
	})

	t.Run("three runs intersection", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
					2: {Index: 2, Order: 20, AccessType: block.Read},
					3: {Index: 3, Order: 30, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 13, AccessType: block.Read},
					2: {Index: 2, Order: 23, AccessType: block.Read},
					4: {Index: 4, Order: 40, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 16, AccessType: block.Read},
					3: {Index: 3, Order: 33, AccessType: block.Read},
					4: {Index: 4, Order: 43, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		// Only index 1 is in all three runs
		assert.Len(t, result, 1)
		assert.Equal(t, uint64(1), result[0].Index)
		// Average order: (10+13+16)/3 = 13
		assert.Equal(t, uint64(13), result[0].Order)
	})

	t.Run("average order rounds down", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 11, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		assert.Len(t, result, 1)
		// (10+11)/2 = 10 (integer division)
		assert.Equal(t, uint64(10), result[0].Order)
	})

	t.Run("three runs with mixed access types", func(t *testing.T) {
		t.Parallel()
		data := []block.PrefetchData{
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Write},
				},
				BlockSize: 4096,
			},
			{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 10, AccessType: block.Read},
				},
				BlockSize: 4096,
			},
		}

		result := computeCommonPrefetchEntries(data)

		assert.Len(t, result, 1)
		// Should be read because second run differs from first
		assert.Equal(t, block.Read, result[0].AccessType)
	})
}
