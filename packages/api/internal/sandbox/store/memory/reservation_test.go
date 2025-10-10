package memory

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	sandboxID = "test-sandbox-id"
)

var teamID = uuid.New()

func newMemoryStore() *Store {
	cache := NewStore(nil, nil)
	return cache
}

func TestReservation(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	assert.NoError(t, err)
}

func TestReservation_Exceeded(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	_, _, err = cache.Reserve(teamID.String(), "sandbox-2", 1)
	require.Error(t, err)
	assert.IsType(t, &sandbox.LimitExceededError{}, err)
}

func TestReservation_SameSandbox(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)

	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	assert.NotNil(t, waitForStart)
}

func TestReservation_Release(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	cache.Remove(teamID.String(), sandboxID)

	_, _, err = cache.Reserve(teamID.String(), sandboxID, 1)
	assert.NoError(t, err)
}

func TestReservation_ResumeAlreadyRunningSandbox(t *testing.T) {
	cache := newMemoryStore()

	data := sandbox.Sandbox{
		ClientID:   consts.ClientID,
		SandboxID:  sandboxID,
		TemplateID: "test",

		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}

	cache.Add(t.Context(), data, false)

	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	assert.NotNil(t, waitForStart)
}

func TestReservation_WaitForStart(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Second call should return waitForStart
	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// Wait should now complete and return the sandbox
	ctx := t.Context()
	result, err := waitForStart(ctx)
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result.SandboxID)
	assert.Equal(t, expectedSbx.TemplateID, result.TemplateID)
}

func TestReservation_WaitForStartError(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Second call should return waitForStart
	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Finish with an error
	expectedErr := assert.AnError
	finishStart(sandbox.Sandbox{}, expectedErr)

	// Wait should return the error
	ctx := t.Context()
	_, err = waitForStart(ctx)
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

func TestReservation_MultipleWaiters(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Multiple calls should all return waitForStart
	_, waitForStart1, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart1)

	_, waitForStart2, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart2)

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// All waiters should get the result
	ctx := t.Context()
	result1, err := waitForStart1(ctx)
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result1.SandboxID)

	result2, err := waitForStart2(ctx)
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result2.SandboxID)
}

func TestReservation_Remove(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// Remove the reservation
	cache.Remove(teamID.String(), sandboxID)

	// Should be able to reserve again
	finishStart2, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart2)
}

func TestReservation_MultipleTeams(t *testing.T) {
	cache := newMemoryStore()

	team1 := uuid.New()
	team2 := uuid.New()
	sandbox1 := "sandbox-1"
	sandbox2 := "sandbox-2"

	// Reserve for team1
	_, _, err := cache.Reserve(team1.String(), sandbox1, 1)
	require.NoError(t, err)

	// Should not affect team2's limit
	_, _, err = cache.Reserve(team2.String(), sandbox2, 1)
	require.NoError(t, err)

	// team1 should be at limit
	_, _, err = cache.Reserve(team1.String(), "sandbox-3", 1)
	require.Error(t, err)
	assert.IsType(t, &sandbox.LimitExceededError{}, err)

	// team2 should also be at limit
	_, _, err = cache.Reserve(team2.String(), "sandbox-4", 1)
	require.Error(t, err)
	assert.IsType(t, &sandbox.LimitExceededError{}, err)
}
