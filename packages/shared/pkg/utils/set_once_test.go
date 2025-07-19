package utils

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSetOnce(t *testing.T) {
	setOnce := NewSetOnce[int]()

	setOnce.SetValue(1)

	value, err := setOnce.Wait()
	assert.Nil(t, err)
	assert.Equal(t, 1, value)

	setOnce.SetValue(2)

	value, err = setOnce.Wait()
	assert.Nil(t, err)
	assert.Equal(t, 1, value)

	setOnce.SetError(fmt.Errorf("error"))

	value, err = setOnce.Wait()
	assert.Nil(t, err)
	assert.Equal(t, 1, value)
}

func TestSetOnceSetError(t *testing.T) {
	setOnce := NewSetOnce[int]()

	setOnce.SetError(fmt.Errorf("error"))

	value, err := setOnce.Wait()
	assert.Error(t, err)
	assert.Equal(t, 0, value)

	setOnce.SetValue(1)

	value, err = setOnce.Wait()
	assert.Error(t, err)
	assert.Equal(t, 0, value)
}

func TestSetOnceWait(t *testing.T) {
	setOnce := NewSetOnce[int]()

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()

		time.Sleep(200 * time.Millisecond)
		setOnce.SetValue(1)
	}()

	value, err := setOnce.Wait()
	assert.Nil(t, err)
	assert.Equal(t, 1, value)

	wg.Wait()
}

func TestSetOnceWaitWithContext(t *testing.T) {
	setOnce := NewSetOnce[int]()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()

		time.Sleep(200 * time.Millisecond)
		setOnce.SetValue(1)
	}()

	value, err := setOnce.WaitWithContext(ctx)
	assert.Nil(t, err)
	assert.Equal(t, 1, value)

	wg.Wait()
}

func TestSetOnceWaitWithContextCanceled(t *testing.T) {
	setOnce := NewSetOnce[int]()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := setOnce.WaitWithContext(ctx)
	assert.Error(t, err)

	wg.Wait()
}

func TestSetOnceSetResultConcurrent(t *testing.T) {
	setOnce := NewSetOnce[int]()

	wg1 := sync.WaitGroup{}
	wg2 := sync.WaitGroup{}
	for i := 1; i < 100; i++ {
		even := i%2 == 0
		if even {
			wg1.Add(1)
		} else {
			wg2.Add(1)
		}
		go func(i int) {
			time.Sleep(time.Microsecond)

			setOnce.SetValue(i)

			if even {
				wg1.Done()
			} else {
				wg2.Done()
			}
		}(i)
	}

	wg1.Wait()

	value, err := setOnce.Wait()
	assert.Nil(t, err)

	assert.LessOrEqual(t, 1, value)
	assert.GreaterOrEqual(t, 99, value)

	wg2.Wait()
}

func TestSetOnceSetResultConcurrentWithContext(t *testing.T) {
	setOnce := NewSetOnce[int]()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg1 := sync.WaitGroup{}
	wg2 := sync.WaitGroup{}
	for i := 1; i < 100; i++ {
		even := i%2 == 0
		if even {
			wg1.Add(1)
		} else {
			wg2.Add(1)
		}
		go func(i int) {
			time.Sleep(time.Microsecond)

			setOnce.SetValue(i)

			if even {
				wg1.Done()
			} else {
				wg2.Done()
			}
		}(i)
	}

	wg1.Wait()

	value, err := setOnce.WaitWithContext(ctx)
	assert.Nil(t, err)

	assert.LessOrEqual(t, 1, value)
	assert.GreaterOrEqual(t, 99, value)

	wg2.Wait()
}

func TestSetOnceConcurrentReads(t *testing.T) {
	setOnce := NewSetOnce[int]()
	const numReaders = 100

	// Set value first
	setOnce.SetValue(42)

	// Start multiple concurrent readers
	var wg sync.WaitGroup
	wg.Add(numReaders)

	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			value, err := setOnce.Wait()
			assert.Nil(t, err)
			assert.Equal(t, 42, value)
		}()
	}

	wg.Wait()
}

func TestSetOnceConcurrentReadsWithContext(t *testing.T) {
	setOnce := NewSetOnce[int]()
	const numReaders = 100

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set value first
	setOnce.SetValue(42)

	// Start multiple concurrent readers
	var wg sync.WaitGroup
	wg.Add(numReaders)

	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			value, err := setOnce.WaitWithContext(ctx)
			assert.Nil(t, err)
			assert.Equal(t, 42, value)
		}()
	}

	wg.Wait()
}

func TestSetOnceConcurrentReadersBeforeWrite(t *testing.T) {
	setOnce := NewSetOnce[int]()
	const numReaders = 50

	// Start readers before the value is set
	var wg sync.WaitGroup
	wg.Add(numReaders)

	results := make(chan int, numReaders)

	// Launch readers
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			value, err := setOnce.Wait()
			assert.Nil(t, err)
			results <- value
		}()
	}

	// Small delay to ensure readers are waiting
	time.Sleep(10 * time.Millisecond)

	// Set the value
	setOnce.SetValue(42)

	// Wait for all readers
	wg.Wait()
	close(results)

	// Verify all readers got the same value
	for value := range results {
		assert.Equal(t, 42, value)
	}
}

func TestSetOnceConcurrentReadWriteRace(t *testing.T) {
	setOnce := NewSetOnce[int]()
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numOperations * 2) // For both readers and writers

	// Launch concurrent readers and writers
	for i := 0; i < numOperations; i++ {
		// Reader
		go func() {
			defer wg.Done()
			value, _ := setOnce.Wait()
			// Value should be either 0 (not set) or 42 (set)
			assert.Contains(t, []int{0, 42}, value)
		}()

		// Writer
		go func() {
			defer wg.Done()
			_ = setOnce.SetValue(42)
		}()
	}

	wg.Wait()

	// Final value should be 42 if any write succeeded
	finalValue, err := setOnce.Wait()
	assert.Nil(t, err)
	assert.Equal(t, 42, finalValue)
}

func TestNotSetResult(t *testing.T) {
	setOnce := NewSetOnce[int]()

	value, err := setOnce.Result()
	assert.Equal(t, 0, value)
	assert.ErrorIs(t, err, ErrNotSet{})
}

func TestResultAfterDone(t *testing.T) {
	setOnce := NewSetOnce[int]()

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		setOnce.SetValue(1)
	}()

	<-setOnce.Done

	wg.Wait()

	value, err := setOnce.Result()
	assert.Equal(t, 1, value)
	assert.NoError(t, err)
}

func TestMultipleDone(t *testing.T) {
	setOnce := NewSetOnce[int]()

	wg := sync.WaitGroup{}

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-setOnce.Done
		}()
	}

	setOnce.SetValue(1)

	wg.Wait()

	value, err := setOnce.Result()
	assert.Equal(t, 1, value)
	assert.NoError(t, err)
}
