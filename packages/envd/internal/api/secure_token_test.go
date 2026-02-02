package api

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecureTokenSetAndEquals(t *testing.T) {
	t.Parallel()

	st := &SecureToken{}

	// Initially not set
	assert.False(t, st.IsSet(), "token should not be set initially")
	assert.False(t, st.Equals("any-token"), "equals should return false when not set")

	// Set token
	err := st.Set("test-token")
	require.NoError(t, err)
	assert.True(t, st.IsSet(), "token should be set after Set()")
	assert.True(t, st.Equals("test-token"), "equals should return true for correct token")
	assert.False(t, st.Equals("wrong-token"), "equals should return false for wrong token")
	assert.False(t, st.Equals(""), "equals should return false for empty token")
}

func TestSecureTokenReplace(t *testing.T) {
	t.Parallel()

	st := &SecureToken{}

	// Set initial token
	err := st.Set("first-token")
	require.NoError(t, err)
	assert.True(t, st.Equals("first-token"))

	// Replace with new token (old one should be destroyed)
	err = st.Set("second-token")
	require.NoError(t, err)
	assert.True(t, st.Equals("second-token"), "should match new token")
	assert.False(t, st.Equals("first-token"), "should not match old token")
}

func TestSecureTokenDestroy(t *testing.T) {
	t.Parallel()

	st := &SecureToken{}

	// Set and then destroy
	err := st.Set("test-token")
	require.NoError(t, err)
	assert.True(t, st.IsSet())

	st.Destroy()
	assert.False(t, st.IsSet(), "token should not be set after Destroy()")
	assert.False(t, st.Equals("test-token"), "equals should return false after Destroy()")

	// Destroy on already destroyed should be safe
	st.Destroy()
	assert.False(t, st.IsSet())
}

func TestSecureTokenBytes(t *testing.T) {
	t.Parallel()

	st := &SecureToken{}

	// Bytes should return error when not set
	_, err := st.Bytes()
	require.ErrorIs(t, err, ErrTokenNotSet)

	// Set token and get bytes
	err = st.Set("test-token")
	require.NoError(t, err)

	bytes, err := st.Bytes()
	require.NoError(t, err)
	assert.Equal(t, []byte("test-token"), bytes)

	// Zero out the bytes (as caller should do)
	for i := range bytes {
		bytes[i] = 0
	}

	// Original should still be intact
	assert.True(t, st.Equals("test-token"), "original token should still work after zeroing copy")

	// After destroy, bytes should fail
	st.Destroy()
	_, err = st.Bytes()
	assert.ErrorIs(t, err, ErrTokenNotSet)
}

func TestSecureTokenConcurrentAccess(t *testing.T) {
	t.Parallel()

	st := &SecureToken{}
	err := st.Set("initial-token")
	require.NoError(t, err)

	var wg sync.WaitGroup
	const numGoroutines = 100

	// Concurrent reads
	for range numGoroutines {
		wg.Go(func() {
			st.IsSet()
			st.Equals("initial-token")
		})
	}

	// Concurrent writes
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			st.Set("token-" + string(rune('a'+idx)))
		}(i)
	}

	wg.Wait()

	// Should still be in a valid state
	assert.True(t, st.IsSet())
}

func TestSecureTokenEmptyToken(t *testing.T) {
	t.Parallel()

	st := &SecureToken{}

	// Setting empty token should return an error
	err := st.Set("")
	require.ErrorIs(t, err, ErrTokenEmpty)
	assert.False(t, st.IsSet(), "token should not be set after empty token error")
}

func TestSecureTokenEmptyTokenDoesNotClearExisting(t *testing.T) {
	t.Parallel()

	st := &SecureToken{}

	// Set a valid token first
	err := st.Set("valid-token")
	require.NoError(t, err)
	assert.True(t, st.IsSet())

	// Attempting to set empty token should fail and preserve existing token
	err = st.Set("")
	require.ErrorIs(t, err, ErrTokenEmpty)
	assert.True(t, st.IsSet(), "existing token should be preserved after empty token error")
	assert.True(t, st.Equals("valid-token"), "existing token value should be unchanged")
}
