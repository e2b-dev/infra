package instance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	sandboxID = "test-sandbox-id"
)

var teamID = uuid.New()

func newInstanceCache() *MemoryStore {
	deleteFunc := func(ctx context.Context, data *InstanceInfo, removeType RemoveType) error { return nil }

	cache := NewStore(deleteFunc, nil, nil, nil)
	return cache
}

func TestReservation(t *testing.T) {
	cache := newInstanceCache()

	_, err := cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
}

func TestReservation_Exceeded(t *testing.T) {
	cache := newInstanceCache()

	_, err := cache.Reserve(sandboxID, teamID, 0)
	require.Error(t, err)
	assert.IsType(t, &SandboxLimitExceededError{}, err)
}

func TestReservation_SameSandbox(t *testing.T) {
	cache := newInstanceCache()

	_, err := cache.Reserve(sandboxID, teamID, 10)
	require.NoError(t, err)

	_, err = cache.Reserve(sandboxID, teamID, 10)
	require.Error(t, err)
	assert.IsType(t, &AlreadyBeingStartedError{}, err)
}

func TestReservation_Release(t *testing.T) {
	cache := newInstanceCache()

	release, err := cache.Reserve(sandboxID, teamID, 1)
	require.NoError(t, err)
	release()

	_, err = cache.Reserve(sandboxID, teamID, 1)
	assert.NoError(t, err)
}

func TestReservation_ResumeAlreadyRunningSandbox(t *testing.T) {
	cache := newInstanceCache()

	info := &InstanceInfo{
		ClientID:   consts.ClientID,
		SandboxID:  sandboxID,
		TemplateID: "test",

		TeamID:            teamID,
		StartTime:         time.Now(),
		endTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	err := cache.Add(t.Context(), info, false)
	require.NoError(t, err)

	_, err = cache.Reserve(sandboxID, teamID, 1)
	require.Error(t, err)
}
