package utils

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtomicMax_NewAtomicMax(t *testing.T) {
	am := NewAtomicMax()
	require.NotNil(t, am)
	require.Equal(t, int64(0), am.val)
}

func TestAtomicMax_Compare_InitialValue(t *testing.T) {
	am := NewAtomicMax()

	// Should succeed when newValue > current
	assert.True(t, am.Compare(10))
	assert.Equal(t, int64(10), am.val)
}

func TestAtomicMax_Compare_EqualValue(t *testing.T) {
	am := NewAtomicMax()
	am.val = 10

	// Should succeed when newValue > current
	assert.True(t, am.Compare(20))
	assert.Equal(t, int64(20), am.val)
}

func TestAtomicMax_Compare_GreaterValue(t *testing.T) {
	am := NewAtomicMax()
	am.val = 10

	// Should fail when newValue < current, keeping the max value
	assert.False(t, am.Compare(5))
	assert.Equal(t, int64(10), am.val)
}

func TestAtomicMax_Compare_NegativeValues(t *testing.T) {
	am := NewAtomicMax()
	am.val = -5

	assert.True(t, am.Compare(-2))
	assert.Equal(t, int64(-2), am.val)
}

func TestAtomicMax_Compare_Concurrent(t *testing.T) {
	am := NewAtomicMax()
	var wg sync.WaitGroup

	// Run 100 goroutines trying to update the value concurrently
	numGoroutines := 100
	wg.Add(numGoroutines)

	for i := range numGoroutines {
		go func(val int64) {
			defer wg.Done()
			am.Compare(val)
		}(int64(i))
	}

	wg.Wait()

	// The final value should be 99 (the maximum value)
	assert.Equal(t, int64(99), am.val)
}
