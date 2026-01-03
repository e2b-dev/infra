package block

import (
	"maps"
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTracker_AddAndHas(t *testing.T) {
	t.Parallel()
	const pageSize = 4096
	tr := NewTracker(pageSize)

	offset := int64(pageSize * 4)

	// Initially should not be marked
	assert.False(t, tr.Has(offset), "Expected offset %d not to be marked initially", offset)

	// After adding, should be marked
	tr.Add(offset)
	assert.True(t, tr.Has(offset), "Expected offset %d to be marked after Add", offset)

	// Other offsets should not be marked
	otherOffsets := []int64{
		0, pageSize, 2 * pageSize, 3 * pageSize, 5 * pageSize, 10 * pageSize,
	}
	for _, other := range otherOffsets {
		if other == offset {
			continue
		}
		assert.False(t, tr.Has(other), "Did not expect offset %d to be marked (only %d should be marked)", other, offset)
	}
}

func TestTracker_Reset(t *testing.T) {
	t.Parallel()
	const pageSize = 4096
	tr := NewTracker(pageSize)

	offset := int64(pageSize * 4)

	// Add offset and verify it's marked
	tr.Add(offset)
	assert.True(t, tr.Has(offset), "Expected offset %d to be marked after Add", offset)

	// After reset, should not be marked
	tr.Reset()
	assert.False(t, tr.Has(offset), "Expected offset %d to be cleared after Reset", offset)

	// Offsets that were never set should also remain unset
	otherOffsets := []int64{0, pageSize, 2 * pageSize, pageSize * 10}
	for _, other := range otherOffsets {
		assert.False(t, tr.Has(other), "Expected offset %d to not be marked after Reset", other)
	}
}

func TestTracker_MultipleOffsets(t *testing.T) {
	t.Parallel()
	const pageSize = 4096
	tr := NewTracker(pageSize)

	offsets := []int64{0, pageSize, 2 * pageSize, 10 * pageSize}

	// Add multiple offsets
	for _, o := range offsets {
		tr.Add(o)
	}

	// Verify all offsets are marked
	for _, o := range offsets {
		assert.True(t, tr.Has(o), "Expected offset %d to be marked", o)
	}

	// Check offsets in between added offsets are not set
	// (Offsets that aren't inside any marked block should not be marked)
	nonSetOffsets := []int64{
		3 * pageSize,
		4 * pageSize,
		5 * pageSize,
		6 * pageSize,
		7 * pageSize,
		8 * pageSize,
		9 * pageSize,
		11 * pageSize,
	}
	for _, off := range nonSetOffsets {
		assert.False(t, tr.Has(off), "Expected offset %d to not be marked (only explicit blocks added)", off)
	}
}

func TestTracker_ResetClearsAll(t *testing.T) {
	t.Parallel()
	const pageSize = 4096
	tr := NewTracker(pageSize)

	offsets := []int64{0, pageSize, 2 * pageSize, 10 * pageSize}

	// Add multiple offsets
	for _, o := range offsets {
		tr.Add(o)
	}

	// Reset should clear all
	tr.Reset()

	// Verify all offsets are cleared
	for _, o := range offsets {
		assert.False(t, tr.Has(o), "Expected offset %d to be cleared after Reset", o)
	}
	// Check unrelated offsets also not marked
	moreOffsets := []int64{3 * pageSize, 7 * pageSize, 100, 4095}
	for _, o := range moreOffsets {
		assert.False(t, tr.Has(o), "Expected offset %d to not be marked after Reset", o)
	}
}

func TestTracker_MisalignedOffset(t *testing.T) {
	t.Parallel()
	const pageSize = 4096
	tr := NewTracker(pageSize)

	// Test with misaligned offset
	misalignedOffset := int64(123)
	tr.Add(misalignedOffset)

	// Should be set for the block containing the offset—that is, block 0 (0..4095)
	assert.True(t, tr.Has(misalignedOffset), "Expected misaligned offset %d to be marked (should mark its containing block)", misalignedOffset)

	// Now check that any offset in the same block is also considered marked
	anotherOffsetInSameBlock := int64(1000)
	assert.True(t, tr.Has(anotherOffsetInSameBlock), "Expected offset %d to be marked as in same block as %d", anotherOffsetInSameBlock, misalignedOffset)

	// But not for a different block
	offsetInNextBlock := int64(pageSize)
	assert.False(t, tr.Has(offsetInNextBlock), "Did not expect offset %d to be marked", offsetInNextBlock)

	// And not far outside any set block
	offsetFar := int64(2 * pageSize)
	assert.False(t, tr.Has(offsetFar), "Did not expect offset %d to be marked", offsetFar)
}

func TestTracker_Offsets(t *testing.T) {
	t.Parallel()
	const pageSize = 4096
	tr := NewTracker(pageSize)

	numOffsets := 300

	offsetsMap := map[int64]struct{}{}

	for range numOffsets {
		select {
		case <-t.Context().Done():
			t.FailNow()
		default:
		}

		base := int64(rand.Intn(121)) // 0..120
		offset := base * pageSize

		offsetsMap[offset] = struct{}{}
		tr.Add(offset)
	}

	expectedOffsets := slices.Collect(maps.Keys(offsetsMap))
	actualOffsets := slices.Collect(tr.Offsets())

	assert.Len(t, actualOffsets, len(expectedOffsets))
	assert.ElementsMatch(t, expectedOffsets, actualOffsets)
}
