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
	t.Parallel()

	t.Run("items are destroyed on quit", func(t *testing.T) {
		t.Parallel()

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

		err = wp.Close(t.Context())
		require.NoError(t, err)

		// verify the item has been released
		assert.Contains(t, released, item.Key)
	})

	t.Run("items are populated on the fresh channel", func(t *testing.T) {
		t.Parallel()

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

		wp.wg.Wait()
	})

	t.Run("populate quits on close", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			testFactory,
		)
		t.Cleanup(closePool(t, wp))

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
		t.Parallel()

		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			5,
			5,
			testFactory,
		)
		t.Cleanup(closePool(t, wp))

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
		t.Parallel()

		// return one error, one good item, then infinite bad items
		testFactory := NewMockItemFactory[*testItem](t)
		testFactory.EXPECT().Create(mock.Anything).Return(nil, errors.New("first failure")).Once()
		testFactory.EXPECT().Create(mock.Anything).Return(&testItem{Key: "good-item"}, nil).Once()
		testFactory.EXPECT().Create(mock.Anything).Return(nil, errors.New("failed to create"))

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			testFactory,
		)
		wp.sleepOnCreateError = time.Millisecond

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

		close(wp.freshItems) // to avoid hanging forever

		item1, ok := <-wp.freshItems
		require.True(t, ok, "fresh channel should have one item")
		assert.Equal(t, "good-item", item1.Key)

		item2, ok := <-wp.freshItems
		require.False(t, ok, "fresh channel should be closed, but got %v instead", item2)
	})

	t.Run("populate keeps fresh items topped off", func(t *testing.T) {
		t.Parallel()

		testFactory := NewMockItemFactory[*testItem](t)

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			testFactory,
		)
		t.Cleanup(closePool(t, wp))

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
	t.Parallel()

	t.Run("get returns an item from the fresh channel", func(t *testing.T) {
		t.Parallel()

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

	t.Run("prefer item from the reuse pool", func(t *testing.T) {
		t.Parallel()

		itemFactory := NewMockItemFactory[*testItem](t)
		itemFactory.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil).Once()

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1,
			itemFactory,
		)

		t.Cleanup(closePool(t, wp))

		// Add items to both pools
		wp.reusableItems <- &testItem{Key: "reusable-1"}
		wp.freshItems <- &testItem{Key: "fresh-1"}

		// Get should return the reusable item first
		item, err := wp.Get(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "reusable-1", item.Key)

		// Verify fresh item is still in the fresh channel
		assert.Len(t, wp.freshItems, 1)
	})

	t.Run("get returns an item from the reuse pool", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

		wp := NewWarmPool[*testItem](
			"test", "prefix",
			1,
			1, // only 1 item can be created before stalling
			nil,
		)

		err := wp.Close(t.Context())
		require.NoError(t, err)

		item, err := wp.Get(t.Context())
		require.ErrorIs(t, err, ErrPoolClosed)
		assert.Nil(t, item)
	})

	t.Run("get returns an error when the context has been canceled", func(t *testing.T) {
		t.Parallel()

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
	t.Parallel()

	t.Run("return does nothing when already closed", func(t *testing.T) {
		t.Parallel()

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

	t.Run("return does nothing when context already canceled", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

		f := NewMockItemFactory[*testItem](t)

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

		// only used during clean up to close the pool
		f.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil).Once()
	})

	t.Run("return destroys item if the pool is already full", func(t *testing.T) {
		t.Parallel()

		f := NewMockItemFactory[*testItem](t)
		f.EXPECT().Destroy(mock.Anything, mock.Anything).Return(nil).Times(2)

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
		wp.returnTimeout = time.Millisecond * 10
		wp.Return(t.Context(), &testItem{})

		wp.wg.Wait()

		assert.Len(t, wp.reusableItems, 1) // ensure the item did not make it to the reuse pool
	})
}

func TestWarmPool_Close(t *testing.T) {
	t.Parallel()

	t.Run("close does not return an error when the pool is already closed", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

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
