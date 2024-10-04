package block

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarker(t *testing.T) {
	size := uint(100)
	marker := NewMarker(size)

	// Test Mark and IsMarked
	offset1 := int64(10)
	marker.Mark(offset1)
	assert.True(t, marker.IsMarked(offset1))

	offset2 := int64(50)
	assert.False(t, marker.IsMarked(offset2))
	marker.Mark(offset2)
	assert.True(t, marker.IsMarked(offset2))

	// Test concurrency
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			offset := int64(i * 10)
			marker.Mark(offset)
			assert.True(t, marker.IsMarked(offset))
		}(i)
	}
	wg.Wait()
}
