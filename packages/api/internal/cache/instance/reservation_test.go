package instance

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sandboxID = "test-sandbox-id"
)

var teamID = uuid.New()

func newInstanceCache() (*InstanceCache, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cache := NewCache(ctx, nil, nil, nil)
	return cache, cancel
}

func TestReservation(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	exceeded, _, err := cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
	assert.False(t, exceeded)
}

func TestReservation_Exceeded(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	exceeded, _, err := cache.Reserve(sandboxID, teamID, 0)
	assert.NoError(t, err)
	assert.True(t, exceeded)
}

func TestReservation_SameSandbox(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	exceeded, _, err := cache.Reserve(sandboxID, teamID, 10)
	assert.NoError(t, err)
	assert.False(t, exceeded)

	exceeded, _, err = cache.Reserve(sandboxID, teamID, 10)
	require.Error(t, err)
	assert.False(t, exceeded)

	var errAlreadyBeingStarted *ErrAlreadyBeingStarted
	assert.ErrorAs(t, err, &errAlreadyBeingStarted)
}

func TestReservation_Release(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	exceeded, release, err := cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
	assert.False(t, exceeded)
	release()

	exceeded, _, err = cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
	assert.False(t, exceeded)
}
