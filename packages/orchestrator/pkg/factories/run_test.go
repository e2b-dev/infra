package factories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
)

func TestShutdownContextDefaultNeverExpires(t *testing.T) {
	t.Parallel()

	ctx, cancel := shutdownContext(cfg.Config{})
	defer cancel()

	_, hasDeadline := ctx.Deadline()
	assert.False(t, hasDeadline, "default shutdown context must not have a deadline")
	require.NoError(t, ctx.Err())

	cancel()
	require.Error(t, ctx.Err(), "shutdown context must still be cancelable")
}

func TestShutdownContextWithDrainTimeout(t *testing.T) {
	t.Parallel()

	timeout := time.Hour
	before := time.Now()

	ctx, cancel := shutdownContext(cfg.Config{ShutdownDrainTimeout: timeout})
	defer cancel()

	deadline, hasDeadline := ctx.Deadline()
	require.True(t, hasDeadline, "shutdown context must carry the configured drain deadline")
	assert.WithinDuration(t, before.Add(timeout), deadline, time.Minute)
	require.NoError(t, ctx.Err())
}

func TestShutdownContextWithDrainTimeoutExpires(t *testing.T) {
	t.Parallel()

	ctx, cancel := shutdownContext(cfg.Config{ShutdownDrainTimeout: time.Nanosecond})
	defer cancel()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown context with drain timeout did not expire")
	}
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
