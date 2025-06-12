package instance

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

const (
	sandboxID = "test-sandbox-id"
)

var teamID = uuid.New()

func newInstanceCache() (*InstanceCache, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cache := NewCache(ctx, noop.Meter{}, nil, nil, nil)
	return cache, cancel
}

func TestReservation(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	_, err := cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
}

func TestReservation_Exceeded(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	_, err := cache.Reserve(sandboxID, teamID, 0)
	assert.Error(t, err)
	assert.IsType(t, &ErrSandboxLimitExceeded{}, err)
}

func TestReservation_SameSandbox(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	_, err := cache.Reserve(sandboxID, teamID, 10)
	assert.NoError(t, err)

	_, err = cache.Reserve(sandboxID, teamID, 10)
	require.Error(t, err)
	assert.IsType(t, &ErrAlreadyBeingStarted{}, err)
}

func TestReservation_Release(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	release, err := cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
	release()

	_, err = cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
}
