package utils

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorCollector(t *testing.T) {
	t.Parallel()

	t.Run("no errors", func(t *testing.T) {
		t.Parallel()

		ec := NewErrorCollector(1)
		err := ec.Wait()
		require.NoError(t, err)
	})

	t.Run("one error", func(t *testing.T) {
		t.Parallel()

		errTarget := errors.New("target error")
		ec := NewErrorCollector(1)
		ec.Go(t.Context(), func() error { return errTarget })
		err := ec.Wait()
		require.Equal(t, errTarget, err)
	})

	t.Run("multiple errors", func(t *testing.T) {
		t.Parallel()

		errTarget1 := errors.New("first error")
		errTarget2 := errors.New("second error")

		ec := NewErrorCollector(2)
		ec.Go(t.Context(), func() error { return errTarget1 })
		ec.Go(t.Context(), func() error { return errTarget2 })
		err := ec.Wait()
		require.ErrorIs(t, err, errTarget1)
		require.ErrorIs(t, err, errTarget2)
	})

	t.Run("waiting can be canceled", func(t *testing.T) {
		t.Parallel()

		ec := NewErrorCollector(1)

		// Block the collector's only slot
		started := make(chan struct{})
		ctx, cancel1 := context.WithCancel(t.Context())
		ec.Go(ctx, func() error {
			close(started)
			<-ctx.Done()

			return nil
		})

		<-started

		// This Go call should block on the semaphore
		var wasCalled bool
		ctx, cancel2 := context.WithCancel(t.Context())
		ec.Go(ctx, func() error {
			wasCalled = true

			return nil
		})

		// Cancel the context while the second goroutine is waiting for the semaphore
		cancel2()

		// Complete the 1st goroutine, which allows Wait to succeed
		cancel1()

		err := ec.Wait()
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, wasCalled)
	})
}
