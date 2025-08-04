package instance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	sandboxID = "test-sandbox-id"
)

var teamID = uuid.New()

func newInstanceCache() (*InstanceCache, context.CancelFunc) {
	createFunc := func(data *InstanceInfo, created bool) error { return nil }
	deleteFunc := func(data *InstanceInfo) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	cache := NewCache(ctx, noop.MeterProvider{}, createFunc, deleteFunc)
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

func TestReservation_ResumeAlreadyRunningSandbox(t *testing.T) {
	cache, cancel := newInstanceCache()
	defer cancel()

	info := &InstanceInfo{
		TeamID:            teamID,
		StartTime:         time.Now(),
		endTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
		Instance: &api.Sandbox{
			ClientID:   consts.ClientID,
			SandboxID:  sandboxID,
			TemplateID: "test",
		},
	}
	err := cache.Add(context.Background(), info, false)
	assert.NoError(t, err)

	_, err = cache.Reserve(sandboxID, teamID, 1)
	assert.Error(t, err)
}
