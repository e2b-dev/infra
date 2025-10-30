package block

import (
	"testing"
)

func TestTracker_AddAndHas(t *testing.T) {
	const pageSize = 4096
	tr := NewTracker(pageSize)

	offset := int64(pageSize * 4)

	// Initially should not be marked
	if tr.Has(offset) {
		t.Errorf("Expected offset %d not to be marked initially", offset)
	}

	// After adding, should be marked
	tr.Add(offset)
	if !tr.Has(offset) {
		t.Errorf("Expected offset %d to be marked after Add", offset)
	}
}

func TestTracker_Reset(t *testing.T) {
	const pageSize = 4096
	tr := NewTracker(pageSize)

	offset := int64(pageSize * 4)

	// Add offset and verify it's marked
	tr.Add(offset)
	if !tr.Has(offset) {
		t.Errorf("Expected offset %d to be marked after Add", offset)
	}

	// After reset, should not be marked
	tr.Reset()
	if tr.Has(offset) {
		t.Errorf("Expected offset %d to be cleared after Reset", offset)
	}
}

func TestTracker_MultipleOffsets(t *testing.T) {
	const pageSize = 4096
	tr := NewTracker(pageSize)

	offsets := []int64{0, pageSize, 2 * pageSize, 10 * pageSize}

	// Add multiple offsets
	for _, o := range offsets {
		tr.Add(o)
	}

	// Verify all offsets are marked
	for _, o := range offsets {
		if !tr.Has(o) {
			t.Errorf("Expected offset %d to be marked", o)
		}
	}
}

func TestTracker_ResetClearsAll(t *testing.T) {
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
		if tr.Has(o) {
			t.Errorf("Expected offset %d to be cleared after Reset", o)
		}
	}
}

func TestTracker_MisalignedOffset(t *testing.T) {
	const pageSize = 4096
	tr := NewTracker(pageSize)

	// Test with misaligned offset
	misalignedOffset := int64(123)
	tr.Add(misalignedOffset)

	// Should be set for the block containing the offset—that is, block 0 (0..4095)
	if !tr.Has(misalignedOffset) {
		t.Errorf("Expected misaligned offset %d to be marked (should mark its containing block)", misalignedOffset)
	}

	// Now check that any offset in the same block is also considered marked
	anotherOffsetInSameBlock := int64(1000)
	if !tr.Has(anotherOffsetInSameBlock) {
		t.Errorf("Expected offset %d to be marked as in same block as %d", anotherOffsetInSameBlock, misalignedOffset)
	}

	// But not for a different block
	offsetInNextBlock := int64(pageSize) // convert to int64 to match Has signature
	if tr.Has(offsetInNextBlock) {
		t.Errorf("Did not expect offset %d to be marked", offsetInNextBlock)
	}
}
