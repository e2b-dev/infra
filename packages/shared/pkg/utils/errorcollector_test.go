package utils

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorCollector(t *testing.T) {
	t.Run("no errors", func(t *testing.T) {
		ec := NewErrorCollector(1)
		err := ec.Wait()
		require.NoError(t, err)
	})

	t.Run("one error", func(t *testing.T) {
		errTarget := errors.New("target error")
		ec := NewErrorCollector(1)
		ec.Go(func() error { return errTarget })
		err := ec.Wait()
		require.Equal(t, errTarget, err)
	})

	t.Run("multiple errors", func(t *testing.T) {
		errTarget1 := errors.New("first error")
		errTarget2 := errors.New("second error")

		ec := NewErrorCollector(2)
		ec.Go(func() error { return errTarget1 })
		ec.Go(func() error { return errTarget2 })
		err := ec.Wait()
		require.ErrorIs(t, err, errTarget1)
		require.ErrorIs(t, err, errTarget2)
	})
}
