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
