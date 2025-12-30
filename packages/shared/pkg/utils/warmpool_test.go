package utils

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type testItem struct{ Key string }

func (ti *testItem) String() string { return ti.Key }

func TestWarmPool_Populate(t *testing.T) {
	t.Run("items are destroyed on quit", func(t *testing.T) {
		item := &testItem{Key: "key"}
		released := make(map[string]struct{})

		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			testFactory,
		)

		// cancel when an item is created, so the next loop will bail
		ctx, cancel := context.WithCancel(t.Context())
		testFactory.EXPECT().Create(mock.Anything).RunAndReturn(func(context.Context) (*testItem, error) {
			cancel()

			return item, nil
		})

		// track which items have been released
		testFactory.EXPECT().Destroy(mock.Anything, item).
			RunAndReturn(func(_ context.Context, s *testItem) error {
				released[s.Key] = struct{}{}

				return nil
			})
		err := wp.Populate(ctx)
		require.ErrorIs(t, err, context.Canceled)

		// verify the item has been released
		assert.Contains(t, released, item.Key)
	})

	t.Run("items are populated on the fresh channel", func(t *testing.T) {
		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			testFactory,
		)

		makeItems := newItemFactory(5)

		testFactory.EXPECT().Create(mock.Anything).RunAndReturn(makeItems)
		testFactory.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil)

		// populate asynchronously
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			err := wp.Populate(ctx)
			assert.ErrorIs(t, err, context.Canceled)
			close(done)
		}()

		item := <-wp.freshItems

		cancel()

		// verify the item has been released
		assert.Equal(t, "test-1", item.Key)

		<-done
	})

	t.Run("populate quits on close", func(t *testing.T) {
		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			testFactory,
		)

		makeItems := newItemFactory(5)

		testFactory.EXPECT().Create(mock.Anything).RunAndReturn(makeItems)
		testFactory.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil)

		// populate asynchronously
		done := make(chan struct{})
		go func() {
			err := wp.Populate(t.Context())
			assert.NoError(t, err)
			close(done)
		}()

		time.Sleep(10 * time.Millisecond) // give it some time to populate

		err := wp.Close(t.Context())
		require.NoError(t, err)

		<-done
	})

	t.Run("populate quits on context cancellation", func(t *testing.T) {
		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			testFactory,
		)

		makeItems := newItemFactory(5)

		testFactory.EXPECT().Create(mock.Anything).RunAndReturn(makeItems)
		testFactory.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil)

		// populate asynchronously
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			err := wp.Populate(ctx)
			assert.ErrorIs(t, err, context.Canceled)
			close(done)
		}()

		time.Sleep(10 * time.Millisecond) // give it some time to populate

		cancel()

		<-done
	})

	t.Run("populate closes the fresh channel on context cancellation", func(t *testing.T) {
		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			5,
			5,
			testFactory,
		)

		makeItems := newItemFactory(5)

		testFactory.EXPECT().Create(mock.Anything).RunAndReturn(makeItems)
		testFactory.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil)

		assert.Empty(t, wp.freshItems, "fresh channel should be empty")

		// populate asynchronously
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			err := wp.Populate(ctx)
			assert.ErrorIs(t, err, context.Canceled)
			close(done)
		}()

		time.Sleep(100 * time.Millisecond)

		assert.Len(t, wp.freshItems, 5, "fresh channel should be populated with 5 items")

		cancel()

		<-done
	})

	t.Run("populate continues when the acquisition fails", func(t *testing.T) {
		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			testFactory,
		)

		makeItems := newItemFactoryFn(func(index int) bool {
			return index%2 == 0
		})

		testFactory.EXPECT().Create(mock.Anything).RunAndReturn(makeItems)

		// populate asynchronously
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			err := wp.Populate(ctx)
			assert.ErrorIs(t, err, context.Canceled)
			close(done)
		}()

		time.Sleep(10 * time.Millisecond)

		cancel()

		<-done

		item, ok := <-wp.freshItems
		if ok {
			assert.False(t, ok, "fresh channel should be closed, but got %s instead", item.Key)
		}
	})

	t.Run("populate keeps fresh items topped off", func(t *testing.T) {
		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			testFactory,
		)

		makeItems := newItemFactory(3)

		testFactory.EXPECT().Create(mock.Anything).RunAndReturn(makeItems)
		testFactory.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil)

		// populate asynchronously
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			err := wp.Populate(ctx)
			assert.ErrorIs(t, err, context.Canceled)
			close(done)
		}()

		time.Sleep(10 * time.Millisecond)

		// first item is quickly populated
		item1, ok := <-wp.freshItems
		require.True(t, ok)
		assert.Equal(t, "test-1", item1.Key)

		// second item is populated after puling the first item
		item2, ok := <-wp.freshItems
		require.True(t, ok)
		assert.Equal(t, "test-2", item2.Key)

		time.Sleep(10 * time.Millisecond)

		cancel()

		<-done
	})
}

func TestWarmPool_Get(t *testing.T) {
	t.Run("get returns an item from the fresh channel", func(t *testing.T) {
		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			nil,
		)
		t.Cleanup(closePool(t, wp))

		wp.freshItems <- &testItem{Key: "test-1"}

		item, err := wp.Get(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test-1", item.Key)
	})

	t.Run("get returns an item from the reuse pool", func(t *testing.T) {
		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			nil,
		)
		t.Cleanup(closePool(t, wp))

		wp.reusableItems <- &testItem{Key: "test-1"}

		item, err := wp.Get(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test-1", item.Key)
	})

	t.Run("get returns an error when the pool is closed", func(t *testing.T) {
		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			nil,
		)

		err := wp.Close(t.Context())
		require.NoError(t, err)

		item, err := wp.Get(t.Context())
		require.ErrorIs(t, err, ErrClosed)
		assert.Nil(t, item)
	})

	t.Run("get returns an error when the context has been canceled", func(t *testing.T) {
		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			nil,
		)
		t.Cleanup(closePool(t, wp))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		item, err := wp.Get(ctx)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, item)
	})
}

func TestWarmPool_Return(t *testing.T) {
	t.Run("return ends when already closed", func(t *testing.T) {
		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			NewMockItemFactory[*testItem](t),
		)

		err := wp.Close(t.Context())
		require.NoError(t, err)

		wp.Return(t.Context(), &testItem{})
		assert.Empty(t, wp.reusableItems) // ensure the item did not make it to the reuse pool
	})

	t.Run("return ends in error when context already canceled", func(t *testing.T) {
		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			NewMockItemFactory[*testItem](t),
		)
		t.Cleanup(closePool(t, wp))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		wp.Return(ctx, &testItem{})
		assert.Empty(t, wp.reusableItems) // ensure the item did not make it to the reuse pool
	})

	t.Run("return returns an item to the reuse pool", func(t *testing.T) {
		f := NewMockItemFactory[*testItem](t)
		f.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			f,
		)
		t.Cleanup(closePool(t, wp))

		wp.Return(t.Context(), &testItem{})

		wp.wg.Wait()

		assert.Len(t, wp.reusableItems, 1) // ensure the item did not make it to the reuse pool
	})

	t.Run("return waits if the pool is already full", func(t *testing.T) {
		f := NewMockItemFactory[*testItem](t)
		f.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			f,
		)
		t.Cleanup(closePool(t, wp))

		// fill up the channel
		wp.reusableItems <- &testItem{}

		// try to return an item
		wp.Return(t.Context(), &testItem{})

		assert.Len(t, wp.reusableItems, 1) // ensure the item did not make it to the reuse pool
	})
}

func TestWarmPool_Close(t *testing.T) {
	t.Run("close does not return an error when the pool is already closed", func(t *testing.T) {
		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			nil,
		)

		err := wp.Close(t.Context())
		require.NoError(t, err)

		err = wp.Close(t.Context())
		require.NoError(t, err)
	})

	t.Run("close destroys resusable items, even if some fail", func(t *testing.T) {
		destroyed := make(map[string]struct{})

		f := NewMockItemFactory[*testItem](t)
		f.EXPECT().Destroy(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, s *testItem) error {
				destroyed[s.Key] = struct{}{}
				if s.Key == "test-2" {
					return errors.New("failed to destroy")
				}

				return nil
			})

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			3,
			1,
			f,
		)

		item1 := &testItem{Key: "test-1"}
		wp.reusableItems <- item1
		item2 := &testItem{Key: "test-2"}
		wp.reusableItems <- item2
		item3 := &testItem{Key: "test-3"}
		wp.reusableItems <- item3

		err := wp.Close(t.Context())
		require.NoError(t, err)

		assert.Len(t, destroyed, 3)
		assert.Empty(t, wp.reusableItems)
	})
}

func closePool(t *testing.T, wp *WarmPool[*testItem]) func() {
	t.Helper()

	return func() {
		t.Helper()

		ctx := context.WithoutCancel(t.Context())
		err := wp.Close(ctx)
		assert.NoError(t, err)
	}
}

func newItemFactory(count int) func(context.Context) (*testItem, error) {
	current := 0

	return func(_ context.Context) (*testItem, error) {
		current++
		if current > count {
			return nil, fmt.Errorf("no more items")
		}

		return &testItem{Key: fmt.Sprintf("test-%d", current)}, nil
	}
}

func newItemFactoryFn(shouldSucceed func(id int) bool) func(context.Context) (*testItem, error) {
	current := 0

	return func(_ context.Context) (*testItem, error) {
		current++
		if !shouldSucceed(current) {
			return nil, fmt.Errorf("no more items")
		}

		return &testItem{Key: fmt.Sprintf("test-%d", current)}, nil
	}
}
