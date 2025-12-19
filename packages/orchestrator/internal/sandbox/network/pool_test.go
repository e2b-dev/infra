package network_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestNetworkPool(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()

		config := network.Config{
			NetworkSlotsPoolSize:      1,
			NetworkSlotsToPrepopulate: 5,
		}
		expected := &network.Slot{Key: "abc-1"}
		closed := atomic.NewBool(true)
		populateDone := atomic.NewBool(false)

		// configure network operations mocks
		operations := NewMockOperations(t)
		operations.EXPECT().
			CreateNetwork(mock.Anything, mock.Anything).
			Return(nil)
		operations.EXPECT().
			RemoveNetwork(mock.Anything, mock.Anything).
			Return(nil)

		// configure storage mocks
		storage := NewMockStorage(t)

		storage.EXPECT().
			Acquire(mock.Anything).
			RunAndReturn(func(context.Context) (*network.Slot, error) {
				return expected, nil
			})

		storage.EXPECT().
			Release(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, s *network.Slot) error {
				assert.Equal(t, expected.Key, s.Key)

				return nil
			})

		p := network.NewPool(operations, storage, config)

		// run populate in the background
		go func() {
			err := p.Populate(t.Context())
			assert.NoError(t, err)
			assert.True(t, closed.Load())
			populateDone.Store(true)
		}()

		// get a network slot
		slot, err := p.Get(t.Context(), &orchestrator.SandboxNetworkConfig{})
		require.NoError(t, err)
		require.NotNil(t, slot)

		// return it
		err = p.Return(t.Context(), slot)
		require.NoError(t, err)

		// close it
		closed.Store(true)
		err = p.Close(t.Context())
		require.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
		assert.True(t, populateDone.Load()) // don't really care when it's over, just make sure it's over
	})
}

func TestNetworkPool_Close(t *testing.T) {
	t.Parallel()

	t.Run("closing releases the reused slots and quits the pool", func(t *testing.T) {
		t.Parallel()

		// data
		reusedSlot := &network.Slot{Key: "abc-1"}
		returned := make(map[string]struct{})
		released := make(map[string]struct{})

		// mocks
		operations := NewMockOperations(t)
		operations.EXPECT().
			RemoveNetwork(mock.Anything, reusedSlot).
			RunAndReturn(func(_ context.Context, slot *network.Slot) error {
				returned[slot.Key] = struct{}{}

				return nil
			})
		storage := NewMockStorage(t)
		storage.EXPECT().
			Release(mock.Anything, reusedSlot).
			RunAndReturn(func(_ context.Context, s *network.Slot) error {
				released[s.Key] = struct{}{}

				return nil
			})

		// setup
		p := network.NewPool(operations, storage, network.Config{
			NetworkSlotsPoolSize:      1,
			NetworkSlotsToPrepopulate: 1,
		})
		err := p.Return(t.Context(), reusedSlot)
		require.NoError(t, err)

		// run the test
		err = p.Close(t.Context())

		// verify
		require.NoError(t, err)
		assert.Len(t, returned, 1)
		assert.Contains(t, returned, reusedSlot.Key)
		assert.Len(t, released, 1)
		assert.Contains(t, released, reusedSlot.Key)
	})

	t.Run("closing logs and does not return failures to clean up slots", func(t *testing.T) {
		t.Parallel()

		// data
		reusedSlot := &network.Slot{Key: "abc-1"}
		returned := make(map[string]struct{})
		released := make(map[string]struct{})

		// mocks
		operations := NewMockOperations(t)
		operations.EXPECT().
			RemoveNetwork(mock.Anything, reusedSlot).
			RunAndReturn(func(_ context.Context, slot *network.Slot) error {
				returned[slot.Key] = struct{}{}

				return errors.New("test error")
			})
		storage := NewMockStorage(t)
		storage.EXPECT().
			Release(mock.Anything, reusedSlot).
			RunAndReturn(func(_ context.Context, s *network.Slot) error {
				released[s.Key] = struct{}{}

				return nil
			})

		// setup
		p := network.NewPool(operations, storage, network.Config{
			NetworkSlotsPoolSize: 1,
		})
		err := p.Return(t.Context(), reusedSlot)
		require.NoError(t, err)

		// run the test
		err = p.Close(t.Context())

		// verify
		require.NoError(t, err)
		assert.Len(t, returned, 1)
		assert.Contains(t, returned, reusedSlot.Key)
		assert.Len(t, released, 1)
		assert.Contains(t, released, reusedSlot.Key)
	})

	t.Run("closing with canceled context quits the pool immediately", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		p := network.NewPool(nil, nil, network.Config{
			NetworkSlotsPoolSize: 1,
		})
		err := p.Close(ctx)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestNetworkPool_Prepopulate(t *testing.T) {
	t.Parallel()

	t.Run("prepopulate closes when the pool closes", func(t *testing.T) {
		t.Parallel()

		// set up data
		errTest := errors.New("test error")

		// set up mocks
		storage := NewMockStorage(t)
		storage.EXPECT().Acquire(mock.Anything).Return(nil, errTest)

		p := network.NewPool(nil, storage, network.Config{
			NetworkSlotsPoolSize:      1,
			NetworkSlotsToPrepopulate: 100,
		})

		go func() {
			err := p.Close(t.Context())
			assert.NoError(t, err)
		}()

		err := p.Populate(t.Context())
		require.NoError(t, err)
	})

	t.Run("prepopulate closes when the context gets canceled", func(t *testing.T) {
		t.Parallel()

		// set up data
		errTest := errors.New("test error")

		// set up mocks
		storage := NewMockStorage(t)
		storage.EXPECT().Acquire(mock.Anything).Return(nil, errTest)

		p := network.NewPool(nil, storage, network.Config{
			NetworkSlotsPoolSize:      1,
			NetworkSlotsToPrepopulate: 10000,
		})

		ctx, cancel := context.WithCancelCause(t.Context())
		sleepTime := 100 * time.Millisecond
		go func() {
			time.Sleep(sleepTime)
			cancel(errTest)
		}()

		start := time.Now()
		err := p.Populate(ctx)
		done := time.Now()
		require.WithinDuration(t, start.Add(sleepTime), done, 20*time.Millisecond)
		require.ErrorIs(t, err, context.Canceled)
		cause := context.Cause(ctx)
		require.ErrorIs(t, cause, errTest)
	})

	t.Run("prepopulate releases slot when closing due to context cancellation", func(t *testing.T) {
		t.Parallel()

		// set up data
		errTest := errors.New("test error")
		slotKey := "abc-1"
		released := make(map[string]struct{})
		removed := make(map[string]struct{})

		// set up mocks
		operations := NewMockOperations(t)
		storage := NewMockStorage(t)
		ctx, cancel := context.WithCancelCause(t.Context())

		// step 1: acquire
		storage.EXPECT().
			Acquire(mock.Anything).
			RunAndReturn(func(context.Context) (*network.Slot, error) {
				return &network.Slot{Key: slotKey}, nil
			})

		// step 2 create
		operations.EXPECT().
			CreateNetwork(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _ *network.Slot) error {
				return nil
			})

		// step 3 cancel
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel(errTest)
		}()

		// step 4 remove
		operations.EXPECT().
			RemoveNetwork(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, s *network.Slot) error {
				assert.Equal(t, slotKey, s.Key)
				removed[s.Key] = struct{}{}

				return nil
			})

		// step 5 release
		storage.EXPECT().
			Release(mock.Anything, &network.Slot{Key: slotKey}).
			RunAndReturn(func(_ context.Context, s *network.Slot) error {
				assert.Equal(t, slotKey, s.Key)
				released[s.Key] = struct{}{}

				return nil
			})

		p := network.NewPool(operations, storage, network.Config{
			NetworkSlotsPoolSize:      1,
			NetworkSlotsToPrepopulate: 1000000,
		})

		// run test
		err := p.Populate(ctx)

		// verify results
		require.ErrorIs(t, err, context.Canceled)
		cause := context.Cause(ctx)
		require.ErrorIs(t, cause, errTest)
		assert.Len(t, released, 1)
		assert.Contains(t, released, slotKey)
		assert.Len(t, removed, 1)
		assert.Contains(t, removed, slotKey)
	})
}

func TestNetworkPool_Get(t *testing.T) {
	t.Parallel()

	t.Run("get returns an error when the pool is closed", func(t *testing.T) {
		t.Parallel()

		p := network.NewPool(nil, nil, network.Config{
			NetworkSlotsPoolSize: 1,
		})
		err := p.Close(t.Context())
		require.NoError(t, err)

		_, err = p.Get(t.Context(), &orchestrator.SandboxNetworkConfig{})
		require.ErrorIs(t, err, network.ErrClosed)
	})

	t.Run("get returns an error when the context is cancelled", func(t *testing.T) {
		t.Parallel()

		p := network.NewPool(nil, nil, network.Config{
			NetworkSlotsPoolSize: 1,
		})

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := p.Get(ctx, &orchestrator.SandboxNetworkConfig{})
		require.ErrorIs(t, err, context.Canceled)
	})
}
