package utils

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorOnce(t *testing.T) {
	t.Parallel()
	errorOnce := NewErrorOnce()

	// Test setting error
	expectedErr := fmt.Errorf("test error")
	err := errorOnce.SetError(expectedErr)
	require.NoError(t, err)

	// Wait should return the error
	err = errorOnce.Wait()
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)

	// Trying to set again should return ErrAlreadySet
	err = errorOnce.SetError(fmt.Errorf("another error"))
	require.ErrorIs(t, err, ErrAlreadySet)

	// Wait should still return the original error
	err = errorOnce.Wait()
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

func TestErrorOnceSetSuccess(t *testing.T) {
	t.Parallel()
	errorOnce := NewErrorOnce()

	// Test setting success (nil error)
	err := errorOnce.SetSuccess()
	require.NoError(t, err)

	// Wait should return nil
	err = errorOnce.Wait()
	require.NoError(t, err)

	// Trying to set error after success should return ErrAlreadySet
	err = errorOnce.SetError(fmt.Errorf("test error"))
	require.ErrorIs(t, err, ErrAlreadySet)

	// Wait should still return nil
	err = errorOnce.Wait()
	require.NoError(t, err)
}
