package block

import (
	"sync"
	"testing"
)

func TestBitset(t *testing.T) {
	b := NewBitset()
	if b == nil {
		t.Error("NewBitset() should not return nil")
	}

	// Test Mark and IsMarked
	offset := int64(1 * Size) // Example offset
	b.Mark(offset)
	if !b.IsMarked(offset) {
		t.Errorf("Mark(%d) was called, but IsMarked(%d) returned false", offset, offset)
	}

	// Test IsMarked for an unmarked offset in the next block
	unmarkedOffset := int64(2 * Size)
	if b.IsMarked(unmarkedOffset) {
		t.Errorf("IsMarked(%d) should return false for unmarked offset", unmarkedOffset)
	}

	// Test concurrent access
	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(off int64) {
			defer wg.Done()
			b.Mark(off)
			if !b.IsMarked(off) {
				t.Errorf("Concurrent Mark(%d)/IsMarked(%d) failed", off, off)
			}
		}(int64(i * 1000))
	}
	wg.Wait()
}
